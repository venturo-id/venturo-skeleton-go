package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"timebox-be/internal/database"
	"timebox-be/internal/modules/core/apikey/domain"
	"timebox-be/internal/modules/core/apikey/dto"
	"timebox-be/pkg/logger"
)

type APIKeyRepository struct {
	db *pgxpool.Pool
}

func NewAPIKeyRepository(db *pgxpool.Pool) *APIKeyRepository {
	return &APIKeyRepository{db: db}
}

// q runs statements on the transaction carried by ctx, if any.
func (r *APIKeyRepository) q(ctx context.Context) database.DBTX {
	return database.Querier(ctx, r.db)
}

// apiKeyColumns matches scanAPIKey.
const apiKeyColumns = `
	id, user_id, key_id, secret_hash, key_prefix,
	name, description, environment, scoped_permissions, ip_whitelist,
	rate_limit, rate_limit_window, expires_at, revoked_at, revoked_by,
	revoke_reason, last_used_at, last_used_ip, total_requests,
	created_at, created_by, updated_at, updated_by`

func scanAPIKey(row pgx.Row) (*domain.APIKey, error) {
	var (
		apiKey      domain.APIKey
		environment string // core.api_key_environment is an enum pgx reads as text
	)
	err := row.Scan(
		&apiKey.ID, &apiKey.UserID, &apiKey.KeyID, &apiKey.SecretHash, &apiKey.KeyPrefix,
		&apiKey.Name, &apiKey.Description, &environment, &apiKey.ScopedPermissions, &apiKey.IPWhitelist,
		&apiKey.RateLimit, &apiKey.RateLimitWindow, &apiKey.ExpiresAt, &apiKey.RevokedAt, &apiKey.RevokedBy,
		&apiKey.RevokeReason, &apiKey.LastUsedAt, &apiKey.LastUsedIP, &apiKey.TotalRequests,
		&apiKey.CreatedAt, &apiKey.CreatedBy, &apiKey.UpdatedAt, &apiKey.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}
	apiKey.Environment = domain.APIKeyEnvironment(environment)
	return &apiKey, nil
}

// Create creates a new API key
func (r *APIKeyRepository) Create(ctx context.Context, apiKey *domain.APIKey) error {
	query := `
		INSERT INTO core.api_keys (
			id, user_id, key_id, secret_hash, key_prefix,
			name, description, environment, scoped_permissions, ip_whitelist,
			rate_limit, rate_limit_window, expires_at,
			created_at, created_by, updated_at, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`

	_, err := r.q(ctx).Exec(ctx, query,
		apiKey.ID, apiKey.UserID, apiKey.KeyID, apiKey.SecretHash, apiKey.KeyPrefix,
		apiKey.Name, apiKey.Description, string(apiKey.Environment), apiKey.ScopedPermissions, apiKey.IPWhitelist,
		apiKey.RateLimit, apiKey.RateLimitWindow, apiKey.ExpiresAt,
		apiKey.CreatedAt, apiKey.CreatedBy, apiKey.UpdatedAt, apiKey.UpdatedBy,
	)
	if err != nil {
		logger.Error("Failed to create API key", logger.Err(err))
		return err
	}

	return nil
}

// findOne returns the live key matching where, or nil.
func (r *APIKeyRepository) findOne(ctx context.Context, where string, args ...any) (*domain.APIKey, error) {
	query := `SELECT ` + apiKeyColumns + ` FROM core.api_keys WHERE ` + where + ` AND deleted_at IS NULL`

	apiKey, err := scanAPIKey(r.q(ctx).QueryRow(ctx, query, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to find API key", logger.Err(err))
		return nil, err
	}
	return apiKey, nil
}

// FindByID finds an API key by ID, restricted to keys owned by userID
func (r *APIKeyRepository) FindByID(ctx context.Context, id, userID string) (*domain.APIKey, error) {
	return r.findOne(ctx, "id = $1 AND user_id = $2", id, userID)
}

// FindByKeyID finds an API key by its key_id. Authentication only: the
// result is not limited to one owner.
func (r *APIKeyRepository) FindByKeyID(ctx context.Context, keyID string) (*domain.APIKey, error) {
	return r.findOne(ctx, "key_id = $1", keyID)
}

// FindAllByUser returns one page of userID's keys, newest first.
func (r *APIKeyRepository) FindAllByUser(ctx context.Context, userID string, params *dto.APIKeyQueryParams) ([]domain.APIKey, int64, error) {
	conditions := []string{"user_id = $1", "deleted_at IS NULL"}
	args := []any{userID}

	if params.Environment != nil {
		args = append(args, *params.Environment)
		conditions = append(conditions, fmt.Sprintf("environment = $%d", len(args)))
	}

	if params.IsActive != nil {
		if *params.IsActive {
			conditions = append(conditions, "revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())")
		} else {
			conditions = append(conditions, "(revoked_at IS NOT NULL OR (expires_at IS NOT NULL AND expires_at <= NOW()))")
		}
	}

	whereClause := strings.Join(conditions, " AND ")

	var total int64
	countQuery := "SELECT COUNT(*) FROM core.api_keys WHERE " + whereClause
	if err := r.q(ctx).QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		logger.Error("Failed to count API keys", logger.Err(err))
		return nil, 0, err
	}

	args = append(args, params.Limit, (params.Page-1)*params.Limit)
	dataQuery := fmt.Sprintf(`
		SELECT %s FROM core.api_keys
		WHERE %s
		ORDER BY created_at DESC, id DESC
		LIMIT $%d OFFSET $%d
	`, apiKeyColumns, whereClause, len(args)-1, len(args))

	rows, err := r.q(ctx).Query(ctx, dataQuery, args...)
	if err != nil {
		logger.Error("Failed to find API keys", logger.Err(err))
		return nil, 0, err
	}
	defer rows.Close()

	apiKeys := make([]domain.APIKey, 0, params.Limit)
	for rows.Next() {
		apiKey, err := scanAPIKey(rows)
		if err != nil {
			logger.Error("Failed to scan API key row", logger.Err(err))
			return nil, 0, err
		}
		apiKeys = append(apiKeys, *apiKey)
	}
	if err := rows.Err(); err != nil {
		logger.Error("Failed to read API keys", logger.Err(err))
		return nil, 0, err
	}

	return apiKeys, total, nil
}

// Update updates an API key. Returns database.ErrNotFound when the key
// does not exist (any more) or belongs to someone else.
func (r *APIKeyRepository) Update(ctx context.Context, apiKey *domain.APIKey) error {
	query := `
		UPDATE core.api_keys
		SET name = $3, description = $4, scoped_permissions = $5, ip_whitelist = $6,
		    rate_limit = $7, rate_limit_window = $8, updated_at = $9, updated_by = $10
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`

	tag, err := r.q(ctx).Exec(ctx, query,
		apiKey.ID, apiKey.UserID,
		apiKey.Name, apiKey.Description, apiKey.ScopedPermissions, apiKey.IPWhitelist,
		apiKey.RateLimit, apiKey.RateLimitWindow, apiKey.UpdatedAt, apiKey.UpdatedBy,
	)
	if err != nil {
		logger.Error("Failed to update API key", logger.Err(err))
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNotFound
	}

	return nil
}

// Revoke revokes an active API key owned by userID. Returns
// database.ErrNotFound when there is no such key that is still active.
func (r *APIKeyRepository) Revoke(ctx context.Context, id, userID, revokedBy string, reason *string) error {
	query := `
		UPDATE core.api_keys
		SET revoked_at = $3, revoked_by = $4, revoke_reason = $5, updated_at = $3, updated_by = $4
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL AND revoked_at IS NULL
	`

	tag, err := r.q(ctx).Exec(ctx, query, id, userID, time.Now(), revokedBy, reason)
	if err != nil {
		logger.Error("Failed to revoke API key", logger.Err(err))
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNotFound
	}

	return nil
}

// RevokeAllByUser revokes every active key of userID, with no revoking
// user (the system did it). Returns how many keys were revoked.
func (r *APIKeyRepository) RevokeAllByUser(ctx context.Context, userID, reason string) (int64, error) {
	query := `
		UPDATE core.api_keys
		SET revoked_at = $2, revoke_reason = $3, updated_at = $2
		WHERE user_id = $1 AND deleted_at IS NULL AND revoked_at IS NULL
	`

	tag, err := r.q(ctx).Exec(ctx, query, userID, time.Now(), reason)
	if err != nil {
		logger.Error("Failed to revoke API keys of user", logger.Err(err))
		return 0, err
	}

	return tag.RowsAffected(), nil
}

// ListAllByUser returns every key of a user, revoked ones included, newest
// first.
func (r *APIKeyRepository) ListAllByUser(ctx context.Context, userID string) ([]domain.APIKey, error) {
	query := `SELECT ` + apiKeyColumns + ` FROM core.api_keys
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC, id DESC`

	rows, err := r.q(ctx).Query(ctx, query, userID)
	if err != nil {
		logger.Error("Failed to list API keys of user", logger.Err(err))
		return nil, err
	}
	defer rows.Close()

	apiKeys := []domain.APIKey{}
	for rows.Next() {
		apiKey, err := scanAPIKey(rows)
		if err != nil {
			logger.Error("Failed to scan API key", logger.Err(err))
			return nil, err
		}
		apiKeys = append(apiKeys, *apiKey)
	}
	if err := rows.Err(); err != nil {
		logger.Error("Failed to read API keys", logger.Err(err))
		return nil, err
	}
	return apiKeys, nil
}

// DeleteAllByUser deletes every key of a user, with the names, IP
// whitelists and last-used addresses they hold. For a closed account.
func (r *APIKeyRepository) DeleteAllByUser(ctx context.Context, userID string) error {
	if _, err := r.q(ctx).Exec(ctx, `DELETE FROM core.api_keys WHERE user_id = $1`, userID); err != nil {
		logger.Error("Failed to delete API keys of user", logger.Err(err))
		return err
	}
	return nil
}

// UpdateUsage updates the usage statistics for an API key
func (r *APIKeyRepository) UpdateUsage(ctx context.Context, keyID, clientIP string) error {
	query := `
		UPDATE core.api_keys
		SET last_used_at = $2, last_used_ip = $3, total_requests = total_requests + 1
		WHERE key_id = $1 AND deleted_at IS NULL
	`

	if _, err := r.q(ctx).Exec(ctx, query, keyID, time.Now(), clientIP); err != nil {
		logger.Error("Failed to update API key usage", logger.Err(err))
		return err
	}

	return nil
}
