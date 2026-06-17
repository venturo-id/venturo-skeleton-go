package service

import (
	"context"
	"errors"
	"regexp"
	"strings"

	clientSvc "venturo-skeleton-go/internal/modules/core/client/service"
	"venturo-skeleton-go/internal/modules/core/translation_overrides/domain"
	"venturo-skeleton-go/internal/modules/core/translation_overrides/dto"
	"venturo-skeleton-go/internal/modules/core/translation_overrides/repository"
	"venturo-skeleton-go/pkg/derrors"
)

// Service errors surfaced to handlers.
var (
	ErrNotFound     = derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, "Translation override not found")
	ErrDuplicateKey = derrors.NewErrorf(derrors.ErrorCodeCustomAlreadyExists, "Translation key already exists for this client")
	ErrInvalidKey   = derrors.NewErrorf(derrors.ErrorCodeCustomBadRequest, "translation_key must match ^[a-z0-9-]+$")
	ErrEmptyValue   = derrors.NewErrorf(derrors.ErrorCodeCustomBadRequest, "value must not be empty")
	ErrClientGone   = derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, "Client not found")
)

// keyPattern mirrors the CHECK constraint in the migration so we fail
// fast with a clean 400 instead of a Postgres constraint error.
var keyPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

type Service struct {
	repo   *repository.Repository
	client *clientSvc.Service
}

func NewService(repo *repository.Repository, client *clientSvc.Service) *Service {
	return &Service{repo: repo, client: client}
}

// Create a new override scoped to the given client.
func (s *Service) Create(
	ctx context.Context,
	clientID string,
	req *dto.CreateTranslationOverrideRequest,
	actorID string,
) (*dto.TranslationOverrideResponse, error) {
	if _, err := s.client.GetByID(ctx, clientID); err != nil {
		if errors.Is(err, clientSvc.ErrNotFound) {
			return nil, ErrClientGone
		}
		return nil, err
	}

	key := strings.TrimSpace(req.TranslationKey)
	if !keyPattern.MatchString(key) {
		return nil, ErrInvalidKey
	}
	if strings.TrimSpace(req.Value) == "" {
		return nil, ErrEmptyValue
	}

	existing, err := s.repo.FindByClientKey(ctx, clientID, key)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	if existing != nil {
		return nil, ErrDuplicateKey
	}

	actor := actorID
	to := &domain.TranslationOverride{
		ClientID:       clientID,
		TranslationKey: key,
		Value:          req.Value,
		Notes:          req.Notes,
		CreatedBy:      &actor,
	}
	if err := s.repo.Create(ctx, to); err != nil {
		return nil, err
	}
	return toResponse(to), nil
}

// GetByKey returns a single override.
func (s *Service) GetByKey(ctx context.Context, clientID, key string) (*dto.TranslationOverrideResponse, error) {
	to, err := s.repo.FindByClientKey(ctx, clientID, key)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return toResponse(to), nil
}

// List returns every override for the given client (admin view).
func (s *Service) List(ctx context.Context, clientID string) (*dto.TranslationOverrideListResponse, error) {
	if _, err := s.client.GetByID(ctx, clientID); err != nil {
		if errors.Is(err, clientSvc.ErrNotFound) {
			return nil, ErrClientGone
		}
		return nil, err
	}

	rows, err := s.repo.FindAllForClient(ctx, clientID)
	if err != nil {
		return nil, err
	}
	items := make([]dto.TranslationOverrideResponse, len(rows))
	for i := range rows {
		items[i] = *toResponse(&rows[i])
	}
	return &dto.TranslationOverrideListResponse{
		Items: items,
		Total: int64(len(items)),
	}, nil
}

// PublicMapBySlug resolves a client by slug, then returns the flat
// key→value shape the FE consumes at bootstrap. Unknown slug returns
// ErrClientGone so handlers can map it to 404.
func (s *Service) PublicMapBySlug(ctx context.Context, slug string) (*dto.PublicTranslationsResponse, error) {
	c, err := s.client.GetBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, clientSvc.ErrNotFound) {
			return nil, ErrClientGone
		}
		return nil, err
	}

	rows, err := s.repo.FindAllForClient(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.TranslationKey] = r.Value
	}
	return &dto.PublicTranslationsResponse{
		ClientID:     c.ID,
		Slug:         c.Slug,
		Translations: out,
	}, nil
}

// Update mutates value/notes for an existing key.
func (s *Service) Update(
	ctx context.Context,
	clientID, key string,
	req *dto.UpdateTranslationOverrideRequest,
	actorID string,
) (*dto.TranslationOverrideResponse, error) {
	if !keyPattern.MatchString(key) {
		return nil, ErrInvalidKey
	}
	if strings.TrimSpace(req.Value) == "" {
		return nil, ErrEmptyValue
	}

	actor := actorID
	to, err := s.repo.UpdateByClientKey(ctx, clientID, key, req.Value, req.Notes, &actor)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return toResponse(to), nil
}

// Delete removes an override ("reset to default").
func (s *Service) Delete(ctx context.Context, clientID, key string) error {
	if err := s.repo.DeleteByClientKey(ctx, clientID, key); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func toResponse(to *domain.TranslationOverride) *dto.TranslationOverrideResponse {
	return &dto.TranslationOverrideResponse{
		ID:             to.ID,
		ClientID:       to.ClientID,
		TranslationKey: to.TranslationKey,
		Value:          to.Value,
		Notes:          to.Notes,
		CreatedAt:      to.CreatedAt,
		CreatedBy:      to.CreatedBy,
		UpdatedAt:      to.UpdatedAt,
		UpdatedBy:      to.UpdatedBy,
	}
}
