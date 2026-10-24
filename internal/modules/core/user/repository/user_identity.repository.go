package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"timebox-be/internal/database"
	"timebox-be/internal/modules/core/user/domain"
	"timebox-be/pkg/logger"
)

// UserIdentityRepository owns reads/writes to core.user_identities.
// The link table is queried twice per Google sign-in (find-by-provider,
// then update-on-login or create). For Google, provider_user_id is the
// Google account id ("sub").
type UserIdentityRepository struct {
	db *pgxpool.Pool
}

func NewUserIdentityRepository(db *pgxpool.Pool) *UserIdentityRepository {
	return &UserIdentityRepository{db: db}
}

// q runs statements on the transaction carried by ctx, if any.
func (r *UserIdentityRepository) q(ctx context.Context) database.DBTX {
	return database.Querier(ctx, r.db)
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
	err := r.q(ctx).QueryRow(ctx, query, provider, providerUserID).Scan(
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
// is enforced at the DB; two concurrent first sign-ins with the same
// Google account make one of them fail with a unique violation, which
// callers check via database.UniqueViolation.
func (r *UserIdentityRepository) Create(ctx context.Context, ui *domain.UserIdentity) error {
	query := `
		INSERT INTO core.user_identities (
			id, user_id, provider, provider_user_id, email, raw_profile,
			last_login_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := r.q(ctx).Exec(ctx, query,
		ui.ID, ui.UserID, ui.Provider, ui.ProviderUserID, ui.Email, ui.RawProfile,
		ui.LastLoginAt, ui.CreatedAt, ui.UpdatedAt,
	)
	if err != nil {
		if _, dup := database.UniqueViolation(err); !dup {
			logger.Error("Failed to create user identity", logger.Err(err))
		}
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

	if _, err := r.q(ctx).Exec(ctx, query, id, email, rawProfile, time.Now()); err != nil {
		logger.Error("Failed to update user identity on login", logger.Err(err))
		return err
	}
	return nil
}

// FindByUser returns the user's identity at provider, or nil. An account
// links at most one account per provider (enforced by the auth service).
func (r *UserIdentityRepository) FindByUser(ctx context.Context, userID, provider string) (*domain.UserIdentity, error) {
	query := `
		SELECT id, user_id, provider, provider_user_id, email, raw_profile,
		       last_login_at, created_at, updated_at
		FROM core.user_identities
		WHERE user_id = $1 AND provider = $2
		ORDER BY created_at DESC, id
		LIMIT 1
	`

	var ui domain.UserIdentity
	err := r.q(ctx).QueryRow(ctx, query, userID, provider).Scan(
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

// DeleteByUserProvider unlinks the user's identities at provider and
// returns how many went.
func (r *UserIdentityRepository) DeleteByUserProvider(ctx context.Context, userID, provider string) (int64, error) {
	tag, err := r.q(ctx).Exec(ctx,
		`DELETE FROM core.user_identities WHERE user_id = $1 AND provider = $2`, userID, provider)
	if err != nil {
		logger.Error("Failed to unlink user identity", logger.Err(err))
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListByUser returns the identities linked to a user, oldest first.
func (r *UserIdentityRepository) ListByUser(ctx context.Context, userID string) ([]domain.UserIdentity, error) {
	query := `
		SELECT id, user_id, provider, provider_user_id, email, raw_profile,
		       last_login_at, created_at, updated_at
		FROM core.user_identities
		WHERE user_id = $1
		ORDER BY created_at, id
	`

	rows, err := r.q(ctx).Query(ctx, query, userID)
	if err != nil {
		logger.Error("Failed to list user identities", logger.Err(err))
		return nil, err
	}
	defer rows.Close()

	identities := []domain.UserIdentity{}
	for rows.Next() {
		var ui domain.UserIdentity
		if err := rows.Scan(
			&ui.ID, &ui.UserID, &ui.Provider, &ui.ProviderUserID,
			&ui.Email, &ui.RawProfile, &ui.LastLoginAt, &ui.CreatedAt, &ui.UpdatedAt,
		); err != nil {
			logger.Error("Failed to scan user identity", logger.Err(err))
			return nil, err
		}
		identities = append(identities, ui)
	}
	if err := rows.Err(); err != nil {
		logger.Error("Failed to read user identities", logger.Err(err))
		return nil, err
	}
	return identities, nil
}

// DeleteByUser unlinks every identity of a user, with the provider
// profile each one kept. For a closed account.
func (r *UserIdentityRepository) DeleteByUser(ctx context.Context, userID string) error {
	if _, err := r.q(ctx).Exec(ctx, `DELETE FROM core.user_identities WHERE user_id = $1`, userID); err != nil {
		logger.Error("Failed to delete user identities", logger.Err(err))
		return err
	}
	return nil
}
