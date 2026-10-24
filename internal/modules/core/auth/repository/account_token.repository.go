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

// AccountTokenRepository owns core.account_tokens: the emailed one-time
// codes (email verification, password reset, email change) and the
// password reset grants.
type AccountTokenRepository struct {
	db *pgxpool.Pool
}

func NewAccountTokenRepository(db *pgxpool.Pool) *AccountTokenRepository {
	return &AccountTokenRepository{db: db}
}

// q runs statements on the transaction carried by ctx, if any.
func (r *AccountTokenRepository) q(ctx context.Context) database.DBTX {
	return database.Querier(ctx, r.db)
}

// Issue stores a new token and retires the user's earlier unused tokens
// for the same purpose, so only the latest code works.
func (r *AccountTokenRepository) Issue(ctx context.Context, t *domain.AccountToken) error {
	return database.WithTx(ctx, r.db, func(ctx context.Context) error {
		// Two codes asked for at once take turns here; otherwise neither
		// would see the other's new token to retire it.
		_, err := r.q(ctx).Exec(ctx, `SELECT FROM core.users WHERE id = $1 FOR NO KEY UPDATE`, t.UserID)
		if err != nil {
			logger.Error("Failed to lock user for an account token", logger.Err(err))
			return err
		}

		_, err = r.q(ctx).Exec(ctx, `
			UPDATE core.account_tokens
			SET used_at = $3
			WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL
		`, t.UserID, string(t.Purpose), t.CreatedAt)
		if err != nil {
			logger.Error("Failed to retire account tokens", logger.Err(err))
			return err
		}

		_, err = r.q(ctx).Exec(ctx, `
			INSERT INTO core.account_tokens (id, user_id, purpose, token_hash, email, new_email, expires_at, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, t.ID, t.UserID, string(t.Purpose), t.TokenHash, t.Email, t.NewEmail, t.ExpiresAt, t.CreatedAt)
		if err != nil {
			logger.Error("Failed to create account token", logger.Err(err))
			return err
		}
		return nil
	})
}

// tokenColumns are the columns scanToken reads, in order.
const tokenColumns = `id, user_id, purpose, token_hash, email, new_email, attempts, expires_at, used_at, created_at`

func scanToken(row pgx.Row) (*domain.AccountToken, error) {
	var (
		t      domain.AccountToken
		stored string
	)
	err := row.Scan(&t.ID, &t.UserID, &stored, &t.TokenHash, &t.Email, &t.NewEmail, &t.Attempts, &t.ExpiresAt, &t.UsedAt, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	t.Purpose = domain.AccountTokenPurpose(stored)
	return &t, nil
}

// FindCurrentForUpdate returns the user's current token for purpose — the
// latest one not used or retired, expired or not — and locks it until the
// transaction in ctx ends, so guesses at a code are checked one at a time.
// Returns nil when there is none.
func (r *AccountTokenRepository) FindCurrentForUpdate(ctx context.Context, userID string, purpose domain.AccountTokenPurpose) (*domain.AccountToken, error) {
	query := `SELECT ` + tokenColumns + `
		FROM core.account_tokens
		WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL
		ORDER BY created_at DESC, id
		LIMIT 1
		FOR UPDATE`

	t, err := scanToken(r.q(ctx).QueryRow(ctx, query, userID, string(purpose)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to find account token", logger.Err(err))
		return nil, err
	}
	return t, nil
}

// CountWrongAttempt adds one wrong guess to a code and returns the count.
func (r *AccountTokenRepository) CountWrongAttempt(ctx context.Context, id string) (int, error) {
	var attempts int
	err := r.q(ctx).QueryRow(ctx,
		`UPDATE core.account_tokens SET attempts = attempts + 1 WHERE id = $1 RETURNING attempts`, id,
	).Scan(&attempts)
	if err != nil {
		logger.Error("Failed to count a wrong code", logger.Err(err))
		return 0, err
	}
	return attempts, nil
}

// MarkUsed marks a token used as of at. It reports false when the token
// was used or retired already.
func (r *AccountTokenRepository) MarkUsed(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx,
		`UPDATE core.account_tokens SET used_at = $2 WHERE id = $1 AND used_at IS NULL`, id, at)
	if err != nil {
		logger.Error("Failed to use account token", logger.Err(err))
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Consume marks a usable token as used and returns it, in one statement:
// of two requests presenting the same token, only one gets it. Returns nil
// when the token is unknown, meant for another purpose, used, or expired.
// For high-entropy tokens looked up by hash (reset grants), not codes.
//
// Run it in the transaction that acts on the token, so a failure later on
// leaves the token usable.
func (r *AccountTokenRepository) Consume(ctx context.Context, purpose domain.AccountTokenPurpose, tokenHash string) (*domain.AccountToken, error) {
	query := `
		UPDATE core.account_tokens
		SET used_at = $3
		WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > $3
		RETURNING ` + tokenColumns

	t, err := scanToken(r.q(ctx).QueryRow(ctx, query, tokenHash, string(purpose), time.Now()))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to consume account token", logger.Err(err))
		return nil, err
	}
	return t, nil
}

// RetireAll makes every unused token of the user — any purpose —
// unusable, as of at. For when access to the account changes hands or
// its password changes: a code asked for before must not outlive that.
func (r *AccountTokenRepository) RetireAll(ctx context.Context, userID string, at time.Time) error {
	_, err := r.q(ctx).Exec(ctx, `
		UPDATE core.account_tokens
		SET used_at = $2
		WHERE user_id = $1 AND used_at IS NULL
	`, userID, at)
	if err != nil {
		logger.Error("Failed to retire account tokens", logger.Err(err))
		return err
	}
	return nil
}

// DeleteExpiredBefore deletes tokens that expired before cutoff and
// returns how many went.
func (r *AccountTokenRepository) DeleteExpiredBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := r.q(ctx).Exec(ctx, `DELETE FROM core.account_tokens WHERE expires_at < $1`, cutoff)
	if err != nil {
		logger.Error("Failed to delete expired account tokens", logger.Err(err))
		return 0, err
	}

	return tag.RowsAffected(), nil
}

// DeleteByUser deletes every token of a user. For a closed account.
func (r *AccountTokenRepository) DeleteByUser(ctx context.Context, userID string) error {
	if _, err := r.q(ctx).Exec(ctx, `DELETE FROM core.account_tokens WHERE user_id = $1`, userID); err != nil {
		logger.Error("Failed to delete account tokens", logger.Err(err))
		return err
	}
	return nil
}
