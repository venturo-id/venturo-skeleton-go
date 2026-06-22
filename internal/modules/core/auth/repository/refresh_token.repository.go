package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go/internal/modules/core/auth/domain"
	"venturo-skeleton-go/pkg/logger"
)

type RefreshTokenRepository struct {
	db *pgxpool.Pool
}

func NewRefreshTokenRepository(db *pgxpool.Pool) *RefreshTokenRepository {
	return &RefreshTokenRepository{db: db}
}

// Create creates a new refresh token
func (r *RefreshTokenRepository) Create(ctx context.Context, token *domain.RefreshToken) error {
	query := `
		INSERT INTO core.refresh_tokens (
			id, user_id, token_hash, device_info, ip_address,
			expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	_, err := r.db.Exec(ctx, query,
		token.ID, token.UserID, token.TokenHash, token.DeviceInfo,
		token.IPAddress, token.ExpiresAt, token.CreatedAt,
	)

	if err != nil {
		logger.Error("Failed to create refresh token", logger.Err(err))
		return err
	}

	return nil
}

// FindByTokenHash finds a refresh token by its hash
func (r *RefreshTokenRepository) FindByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	query := `
		SELECT id, user_id, token_hash, device_info, ip_address,
		       expires_at, revoked_at, last_used_at, created_at
		FROM core.refresh_tokens
		WHERE token_hash = $1
	`

	var token domain.RefreshToken
	err := r.db.QueryRow(ctx, query, tokenHash).Scan(
		&token.ID, &token.UserID, &token.TokenHash, &token.DeviceInfo,
		&token.IPAddress, &token.ExpiresAt, &token.RevokedAt,
		&token.LastUsedAt, &token.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to find refresh token by hash", logger.Err(err))
		return nil, err
	}

	return &token, nil
}

// FindByUserID finds all refresh tokens for a user
func (r *RefreshTokenRepository) FindByUserID(ctx context.Context, userID string) ([]domain.RefreshToken, error) {
	query := `
		SELECT id, user_id, token_hash, device_info, ip_address,
		       expires_at, revoked_at, last_used_at, created_at
		FROM core.refresh_tokens
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC
	`

	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		logger.Error("Failed to find refresh tokens by user ID", logger.Err(err))
		return nil, err
	}
	defer rows.Close()

	var tokens []domain.RefreshToken
	for rows.Next() {
		var token domain.RefreshToken
		err := rows.Scan(
			&token.ID, &token.UserID, &token.TokenHash, &token.DeviceInfo,
			&token.IPAddress, &token.ExpiresAt, &token.RevokedAt,
			&token.LastUsedAt, &token.CreatedAt,
		)
		if err != nil {
			logger.Error("Failed to scan refresh token", logger.Err(err))
			return nil, err
		}
		tokens = append(tokens, token)
	}

	return tokens, nil
}

// UpdateLastUsed updates the last used timestamp
func (r *RefreshTokenRepository) UpdateLastUsed(ctx context.Context, id string) error {
	query := `UPDATE core.refresh_tokens SET last_used_at = $2 WHERE id = $1`

	_, err := r.db.Exec(ctx, query, id, time.Now())
	if err != nil {
		logger.Error("Failed to update last used", logger.Err(err))
		return err
	}

	return nil
}

// Revoke revokes a refresh token
func (r *RefreshTokenRepository) Revoke(ctx context.Context, id string) error {
	query := `UPDATE core.refresh_tokens SET revoked_at = $2 WHERE id = $1`

	_, err := r.db.Exec(ctx, query, id, time.Now())
	if err != nil {
		logger.Error("Failed to revoke refresh token", logger.Err(err))
		return err
	}

	return nil
}

// RevokeByIDAndUser revokes a single refresh token only if it belongs to
// userID. The user_id is part of the WHERE clause (not a post-fetch
// check), so one user can never revoke another's session — the UPDATE
// simply matches zero rows. Returns whether a row was actually revoked
// so the caller can answer 404 on a miss (avoids leaking which ids exist)
// and treat an already-revoked / unknown id the same way.
func (r *RefreshTokenRepository) RevokeByIDAndUser(ctx context.Context, id, userID string) (bool, error) {
	query := `
		UPDATE core.refresh_tokens
		SET revoked_at = $3
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`

	tag, err := r.db.Exec(ctx, query, id, userID, time.Now())
	if err != nil {
		logger.Error("Failed to revoke refresh token by id and user", logger.Err(err))
		return false, err
	}

	return tag.RowsAffected() > 0, nil
}

// RevokeByTokenHash revokes a refresh token by its hash
func (r *RefreshTokenRepository) RevokeByTokenHash(ctx context.Context, tokenHash string) error {
	query := `UPDATE core.refresh_tokens SET revoked_at = $2 WHERE token_hash = $1`

	_, err := r.db.Exec(ctx, query, tokenHash, time.Now())
	if err != nil {
		logger.Error("Failed to revoke refresh token by hash", logger.Err(err))
		return err
	}

	return nil
}

// RevokeAllByUserID revokes all refresh tokens for a user
func (r *RefreshTokenRepository) RevokeAllByUserID(ctx context.Context, userID string) error {
	query := `
		UPDATE core.refresh_tokens
		SET revoked_at = $2
		WHERE user_id = $1 AND revoked_at IS NULL
	`

	_, err := r.db.Exec(ctx, query, userID, time.Now())
	if err != nil {
		logger.Error("Failed to revoke all refresh tokens", logger.Err(err))
		return err
	}

	return nil
}

// DeleteExpired deletes expired refresh tokens
func (r *RefreshTokenRepository) DeleteExpired(ctx context.Context) error {
	query := `DELETE FROM core.refresh_tokens WHERE expires_at < $1`

	_, err := r.db.Exec(ctx, query, time.Now())
	if err != nil {
		logger.Error("Failed to delete expired refresh tokens", logger.Err(err))
		return err
	}

	return nil
}
