package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go-simple/internal/modules/core/user/domain"
	"venturo-skeleton-go-simple/pkg/logger"
)

// UserIdentityRepository owns reads/writes to core.user_identities.
// The link table is queried twice per Google sign-in (find-by-provider,
// then update-on-login or create), so the repo intentionally has just
// the three methods the auth service needs.
type UserIdentityRepository struct {
	db *pgxpool.Pool
}

func NewUserIdentityRepository(db *pgxpool.Pool) *UserIdentityRepository {
	return &UserIdentityRepository{db: db}
}

// FindByProvider looks up an identity by (provider, provider_user_id).
// Returns (nil, nil) when no match — callers branch on that to decide
// between "returning user" and "new user".
func (r *UserIdentityRepository) FindByProvider(ctx context.Context, provider, providerUserID string) (*domain.UserIdentity, error) {
	query := `
		SELECT id, user_id, provider, provider_user_id, email, raw_profile,
		       last_login_at, created_at, updated_at
		FROM core.user_identities
		WHERE provider = $1 AND provider_user_id = $2
	`

	var ui domain.UserIdentity
	err := r.db.QueryRow(ctx, query, provider, providerUserID).Scan(
		&ui.ID, &ui.UserID, &ui.Provider, &ui.ProviderUserID,
		&ui.Email, &ui.RawProfile, &ui.LastLoginAt, &ui.CreatedAt, &ui.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to find user identity", logger.Err(err))
		return nil, err
	}
	return &ui, nil
}

// Create inserts a fresh identity link. UNIQUE(provider, provider_user_id)
// is enforced at the DB; a constraint violation here is a real bug
// (race or stale read), so we just propagate the error.
func (r *UserIdentityRepository) Create(ctx context.Context, ui *domain.UserIdentity) error {
	query := `
		INSERT INTO core.user_identities (
			id, user_id, provider, provider_user_id, email, raw_profile,
			last_login_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := r.db.Exec(ctx, query,
		ui.ID, ui.UserID, ui.Provider, ui.ProviderUserID, ui.Email, ui.RawProfile,
		ui.LastLoginAt, ui.CreatedAt, ui.UpdatedAt,
	)
	if err != nil {
		logger.Error("Failed to create user identity", logger.Err(err))
		return err
	}
	return nil
}

// UpdateOnLogin refreshes the snapshot fields (email, raw_profile,
// last_login_at) for an existing identity. Called on every successful
// Google sign-in so the local snapshot tracks the provider state.
func (r *UserIdentityRepository) UpdateOnLogin(ctx context.Context, id string, email *string, rawProfile []byte) error {
	query := `
		UPDATE core.user_identities
		SET email = $2, raw_profile = $3, last_login_at = $4, updated_at = $4
		WHERE id = $1
	`

	now := time.Now()
	_, err := r.db.Exec(ctx, query, id, email, rawProfile, now)
	if err != nil {
		logger.Error("Failed to update user identity on login", logger.Err(err))
		return err
	}
	return nil
}
