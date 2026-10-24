package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"timebox-be/internal/database"
	"timebox-be/internal/modules/core/auth/domain"
	"timebox-be/pkg/logger"
)

type RefreshTokenRepository struct {
	db *pgxpool.Pool
}

func NewRefreshTokenRepository(db *pgxpool.Pool) *RefreshTokenRepository {
	return &RefreshTokenRepository{db: db}
}

// q runs statements on the transaction carried by ctx, if any.
func (r *RefreshTokenRepository) q(ctx context.Context) database.DBTX {
	return database.Querier(ctx, r.db)
}

// refreshTokenColumns matches scanRefreshToken.
const refreshTokenColumns = `
	id, user_id, token_hash, device_info, ip_address,
	expires_at, revoked_at, last_used_at, created_at,
	session_id, signed_in_at`

func scanRefreshToken(row pgx.Row) (*domain.RefreshToken, error) {
	var token domain.RefreshToken
	err := row.Scan(
		&token.ID, &token.UserID, &token.TokenHash, &token.DeviceInfo,
		&token.IPAddress, &token.ExpiresAt, &token.RevokedAt,
		&token.LastUsedAt, &token.CreatedAt,
		&token.SessionID, &token.SignedInAt,
	)
	if err != nil {
		return nil, err
	}
	return &token, nil
}

// Create creates a new refresh token
func (r *RefreshTokenRepository) Create(ctx context.Context, token *domain.RefreshToken) error {
	query := `
		INSERT INTO core.refresh_tokens (
			id, user_id, token_hash, device_info, ip_address,
			expires_at, created_at, rotated_from_id, session_id, signed_in_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err := r.q(ctx).Exec(ctx, query,
		token.ID, token.UserID, token.TokenHash, token.DeviceInfo,
		token.IPAddress, token.ExpiresAt, token.CreatedAt, token.RotatedFromID,
		token.SessionID, token.SignedInAt,
	)
	if err != nil {
		logger.Error("Failed to create refresh token", logger.Err(err))
		return err
	}

	return nil
}

// FindByTokenHash finds a refresh token by its hash
func (r *RefreshTokenRepository) FindByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	query := `SELECT ` + refreshTokenColumns + ` FROM core.refresh_tokens WHERE token_hash = $1`

	token, err := scanRefreshToken(r.q(ctx).QueryRow(ctx, query, tokenHash))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to find refresh token by hash", logger.Err(err))
		return nil, err
	}

	return token, nil
}

// Claim revokes a usable refresh token and returns it, in one statement:
// of two requests presenting the same token, only one gets a row back.
// Returns nil when the token is unknown, already revoked, or expired.
func (r *RefreshTokenRepository) Claim(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	query := `
		UPDATE core.refresh_tokens
		SET revoked_at = $2, last_used_at = $2
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > $2
		RETURNING ` + refreshTokenColumns

	token, err := scanRefreshToken(r.q(ctx).QueryRow(ctx, query, tokenHash, time.Now()))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to claim refresh token", logger.Err(err))
		return nil, err
	}

	return token, nil
}

// WasRotated reports whether a refresh token was exchanged for a newer one
// (as opposed to revoked by logout).
func (r *RefreshTokenRepository) WasRotated(ctx context.Context, id string) (bool, error) {
	var rotated bool
	err := r.q(ctx).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM core.refresh_tokens WHERE rotated_from_id = $1)`,
		id,
	).Scan(&rotated)
	if err != nil {
		logger.Error("Failed to check refresh token rotation", logger.Err(err))
		return false, err
	}
	return rotated, nil
}

// RevokeByTokenHash revokes one of userID's refresh tokens that is still
// active, and returns the session it ended ("" when nothing was revoked).
// A token already revoked keeps its original revoked_at, which the reuse
// check in the auth service measures from; a token belonging to someone
// else is left alone.
func (r *RefreshTokenRepository) RevokeByTokenHash(ctx context.Context, userID, tokenHash string) (string, error) {
	query := `
		UPDATE core.refresh_tokens
		SET revoked_at = $3
		WHERE token_hash = $1 AND user_id = $2 AND revoked_at IS NULL
		RETURNING session_id
	`

	var sessionID string
	err := r.q(ctx).QueryRow(ctx, query, tokenHash, userID, time.Now()).Scan(&sessionID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		logger.Error("Failed to revoke refresh token by hash", logger.Err(err))
		return "", err
	}

	return sessionID, nil
}

// ListActiveByUser returns the token that keeps each of userID's live
// sessions going — one per session, since rotation revokes the token it
// replaces — most recently used first.
func (r *RefreshTokenRepository) ListActiveByUser(ctx context.Context, userID string) ([]domain.RefreshToken, error) {
	query := `SELECT ` + refreshTokenColumns + `
		FROM core.refresh_tokens
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > $2
		ORDER BY created_at DESC, id DESC`

	rows, err := r.q(ctx).Query(ctx, query, userID, time.Now())
	if err != nil {
		logger.Error("Failed to list sessions", logger.Err(err))
		return nil, err
	}
	defer rows.Close()

	tokens := []domain.RefreshToken{}
	for rows.Next() {
		token, err := scanRefreshToken(rows)
		if err != nil {
			logger.Error("Failed to scan session", logger.Err(err))
			return nil, err
		}
		tokens = append(tokens, *token)
	}
	if err := rows.Err(); err != nil {
		logger.Error("Failed to read sessions", logger.Err(err))
		return nil, err
	}

	return tokens, nil
}

// EndSession revokes the refresh token of one of userID's sessions. It
// returns database.ErrNotFound when the user has no such live session
// (unknown, ended or expired).
func (r *RefreshTokenRepository) EndSession(ctx context.Context, userID, sessionID string) error {
	query := `
		UPDATE core.refresh_tokens
		SET revoked_at = $3
		WHERE user_id = $1 AND session_id = $2 AND revoked_at IS NULL AND expires_at > $3
	`

	tag, err := r.q(ctx).Exec(ctx, query, userID, sessionID, time.Now())
	if err != nil {
		logger.Error("Failed to end session", logger.Err(err))
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNotFound
	}

	return nil
}

// DeleteAllByUser deletes every refresh token of a user, with the device
// and IP address each one recorded. For a closed account.
func (r *RefreshTokenRepository) DeleteAllByUser(ctx context.Context, userID string) error {
	if _, err := r.q(ctx).Exec(ctx, `DELETE FROM core.refresh_tokens WHERE user_id = $1`, userID); err != nil {
		logger.Error("Failed to delete refresh tokens", logger.Err(err))
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

	if _, err := r.q(ctx).Exec(ctx, query, userID, time.Now()); err != nil {
		logger.Error("Failed to revoke all refresh tokens", logger.Err(err))
		return err
	}

	return nil
}

// DeleteExpiredBefore deletes refresh tokens that expired before cutoff
// and returns how many went. An expired token can no longer be claimed,
// so keeping it only grows the table.
func (r *RefreshTokenRepository) DeleteExpiredBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := r.q(ctx).Exec(ctx, `DELETE FROM core.refresh_tokens WHERE expires_at < $1`, cutoff)
	if err != nil {
		logger.Error("Failed to delete expired refresh tokens", logger.Err(err))
		return 0, err
	}

	return tag.RowsAffected(), nil
}
