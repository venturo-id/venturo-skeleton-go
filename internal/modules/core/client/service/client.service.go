package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"venturo-skeleton-go/internal/modules/core/client/domain"
	"venturo-skeleton-go/internal/modules/core/client/dto"
	"venturo-skeleton-go/internal/modules/core/client/repository"
	"venturo-skeleton-go/pkg/derrors"
)

// Service errors surfaced to handlers / auth.
var (
	ErrNotFound       = derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, "Client not found")
	ErrInvalidSlug    = derrors.NewErrorf(derrors.ErrorCodeCustomBadRequest, "slug must be 3–63 chars, lowercase alphanumerics and hyphens, starting/ending with alphanumeric")
	ErrSlugTaken      = derrors.NewErrorf(derrors.ErrorCodeCustomAlreadyExists, "Slug is already taken")
	ErrOwnerResolvUnk = derrors.NewErrorf(derrors.ErrorCodeUnknown, "could not resolve client for user")
)

// slugPattern mirrors the CHECK constraint in migration 012.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

type Service struct {
	repo *repository.Repository
}

func NewService(repo *repository.Repository) *Service {
	return &Service{repo: repo}
}

// CreateForUserTx atomically creates a new client for the given user as
// part of an outer transaction (intended for auth.SignUp when/if it
// ever wraps multiple inserts in a tx). Slug is derived from username;
// collisions resolve by appending `-{hex}`.
func (s *Service) CreateForUserTx(
	ctx context.Context,
	tx pgx.Tx,
	userID, username, displayName string,
) (*domain.Client, error) {
	return s.createForUser(ctx, tx, userID, username, displayName)
}

// CreateForUser is the non-transactional shortcut used by the current
// SignUp flow, which performs sequential inserts on the pool. Prefer
// CreateForUserTx once signup is wrapped in a single tx.
func (s *Service) CreateForUser(
	ctx context.Context,
	userID, username, displayName string,
) (*domain.Client, error) {
	return s.createForUser(ctx, poolExecerFromRepo{repo: s.repo}, userID, username, displayName)
}

func (s *Service) createForUser(
	ctx context.Context,
	q repository.Execer,
	userID, username, displayName string,
) (*domain.Client, error) {
	slug, err := s.allocateSlug(ctx, q, username)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(displayName)
	if name == "" {
		name = slug
	}

	c := &domain.Client{
		ID:          uuid.New().String(),
		Slug:        slug,
		Name:        name,
		OwnerUserID: &userID,
		IsActive:    true,
		CreatedBy:   &userID,
	}
	if err := s.repo.CreateTx(ctx, q, c); err != nil {
		return nil, err
	}
	return c, nil
}

// allocateSlug derives a DNS-safe slug from username, retrying with a
// short random suffix on collision. Tries the base slug first so the
// common case (unique username) lands with a clean slug.
func (s *Service) allocateSlug(ctx context.Context, q repository.Execer, username string) (string, error) {
	base := sanitizeSlug(username)
	if base == "" {
		base = "client-" + shortHex(4)
	}

	candidate := base
	for attempt := 0; attempt < 5; attempt++ {
		exists, err := s.repo.SlugExists(ctx, q, candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
		// Keep within 63 chars: base truncated + "-" + 4 hex
		suffix := "-" + shortHex(4)
		maxBase := 63 - len(suffix)
		if len(base) > maxBase {
			base = base[:maxBase]
		}
		candidate = base + suffix
	}
	return "", derrors.NewErrorf(derrors.ErrorCodeUnknown, "could not allocate unique slug after retries")
}

// poolExecerFromRepo is a thin Execer adapter that forwards calls to
// the repo's embedded pool — used by CreateForUser so the non-tx path
// and the tx path share one implementation.
type poolExecerFromRepo struct {
	repo *repository.Repository
}

func (p poolExecerFromRepo) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return p.repo.Pool().QueryRow(ctx, sql, args...)
}

func (p poolExecerFromRepo) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return p.repo.Pool().Exec(ctx, sql, args...)
}

// ResolveForUser returns the client a user belongs to. Resolution order:
//  1. Client where owner_user_id = userID (registrants see their own client)
//  2. Client of the user's primary company (invited members)
//
// `primaryClientID` is supplied by the caller (from company_users +
// companies) to keep this package dependency-free. Nil primaryClientID
// means "no primary company" — in that case step 1 is the only hope.
func (s *Service) ResolveForUser(
	ctx context.Context,
	userID string,
	primaryClientID *string,
) (*domain.Client, error) {
	// Prefer owned client — the registrant's own tenant always wins.
	if c, err := s.repo.FindByOwnerUserID(ctx, userID); err != nil {
		return nil, err
	} else if c != nil {
		return c, nil
	}
	if primaryClientID != nil {
		return s.GetByID(ctx, *primaryClientID)
	}
	return nil, ErrOwnerResolvUnk
}

// GetByID returns a client by id (non-deleted).
func (s *Service) GetByID(ctx context.Context, id string) (*domain.Client, error) {
	c, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

// GetBySlug returns a client by slug (non-deleted).
func (s *Service) GetBySlug(ctx context.Context, slug string) (*domain.Client, error) {
	c, err := s.repo.FindBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

// List returns all clients (super_admin panel).
func (s *Service) List(ctx context.Context) (*dto.ClientListResponse, error) {
	rows, err := s.repo.FindAll(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]dto.ClientResponse, len(rows))
	for i := range rows {
		items[i] = toResponse(&rows[i])
	}
	return &dto.ClientListResponse{Items: items, Total: int64(len(items))}, nil
}

// Update renames / re-slugs / toggles a client. Super_admin only.
func (s *Service) Update(
	ctx context.Context,
	id string,
	req *dto.UpdateClientRequest,
	actorID string,
) (*dto.ClientResponse, error) {
	if req.Slug != nil {
		slug := strings.ToLower(strings.TrimSpace(*req.Slug))
		if !slugPattern.MatchString(slug) {
			return nil, ErrInvalidSlug
		}
		existing, err := s.repo.FindBySlug(ctx, slug)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
		if existing != nil && existing.ID != id {
			return nil, ErrSlugTaken
		}
		*req.Slug = slug
	}

	c, err := s.repo.Update(ctx, id, req.Name, req.Slug, req.IsActive, actorID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	resp := toResponse(c)
	return &resp, nil
}

// GetRepository exposes the repo so callers (translation_overrides, company)
// can perform cheap lookups without re-plumbing wrappers through service.
func (s *Service) GetRepository() *repository.Repository {
	return s.repo
}

// ResponseFromDomain is a small convenience for adapters that have a domain
// value and want to emit the DTO shape.
func ResponseFromDomain(c *domain.Client) dto.ClientResponse {
	return toResponse(c)
}

// -------- helpers --------

// sanitizeSlug lowercases, replaces non-alnum runs with '-', strips
// leading/trailing '-' and clamps length. Returns empty string if the
// result is < 3 chars (caller fallbacks to a generated slug).
func sanitizeSlug(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	b := make([]byte, 0, len(lower))
	for i := 0; i < len(lower); i++ {
		ch := lower[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			b = append(b, ch)
		} else {
			b = append(b, '-')
		}
	}
	s := collapseHyphens(string(b))
	s = strings.Trim(s, "-")
	if len(s) < 3 {
		return ""
	}
	if len(s) > 63 {
		s = s[:63]
		s = strings.Trim(s, "-")
	}
	return s
}

func collapseHyphens(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	prevHyphen := false
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			if prevHyphen {
				continue
			}
			prevHyphen = true
		} else {
			prevHyphen = false
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

func shortHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func toResponse(c *domain.Client) dto.ClientResponse {
	return dto.ClientResponse{
		ID:          c.ID,
		Slug:        c.Slug,
		Name:        c.Name,
		OwnerUserID: c.OwnerUserID,
		IsActive:    c.IsActive,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}
