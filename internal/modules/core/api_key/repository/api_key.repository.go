package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go/internal/modules/core/api_key/domain"
	"venturo-skeleton-go/pkg/derrors"
	"venturo-skeleton-go/pkg/logger"
)

type ApiKeyRepository struct {
	db *pgxpool.Pool
}

func NewApiKeyRepository(db *pgxpool.Pool) *ApiKeyRepository {
	return &ApiKeyRepository{db: db}
}

// Create creates a new API key
func (r *ApiKeyRepository) Create(ctx context.Context, apiKey *domain.ApiKey) error {
	query := `
		INSERT INTO core.api_keys (
			id, user_id, company_id, key_id, secret_hash, key_prefix,
			name, description, environment, scoped_permissions, ip_whitelist,
			rate_limit, rate_limit_window, expires_at,
			created_at, created_by, updated_at, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	`

	_, err := r.db.Exec(ctx, query,
		apiKey.ID, apiKey.UserID, apiKey.CompanyID, apiKey.KeyID, apiKey.SecretHash, apiKey.KeyPrefix,
		apiKey.Name, apiKey.Description, apiKey.Environment, apiKey.ScopedPermissions, apiKey.IPWhitelist,
		apiKey.RateLimit, apiKey.RateLimitWindow, apiKey.ExpiresAt,
		apiKey.CreatedAt, apiKey.CreatedBy, apiKey.UpdatedAt, apiKey.UpdatedBy,
	)

	if err != nil {
		logger.Error("Failed to create API key", logger.Err(err))
		return err
	}

	return nil
}

// FindByID finds an API key by ID
func (r *ApiKeyRepository) FindByID(ctx context.Context, id, companyID string) (*domain.ApiKey, error) {
	query := `
		SELECT id, user_id, company_id, key_id, secret_hash, key_prefix,
		       name, description, environment, scoped_permissions, ip_whitelist,
		       rate_limit, rate_limit_window, expires_at, revoked_at, revoked_by,
		       revoke_reason, last_used_at, last_used_ip, total_requests,
		       created_at, created_by, updated_at, updated_by
		FROM core.api_keys
		WHERE id = $1 AND company_id = $2 AND deleted_at IS NULL
	`

	apiKey, err := r.scanApiKey(ctx, query, id, companyID)
	if err != nil {
		return nil, err
	}
	if apiKey == nil {
		// Caller asked for one specific key by id → a missing row is a
		// genuine NotFound (the shared scanApiKey collapses ErrNoRows to
		// nil for the FindByKeyID auth probe, so wrap it here).
		return nil, derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, "API key %s not found", id)
	}
	return apiKey, nil
}

// FindByKeyID finds an API key by its key_id (for authentication)
func (r *ApiKeyRepository) FindByKeyID(ctx context.Context, keyID string) (*domain.ApiKey, error) {
	query := `
		SELECT id, user_id, company_id, key_id, secret_hash, key_prefix,
		       name, description, environment, scoped_permissions, ip_whitelist,
		       rate_limit, rate_limit_window, expires_at, revoked_at, revoked_by,
		       revoke_reason, last_used_at, last_used_ip, total_requests,
		       created_at, created_by, updated_at, updated_by
		FROM core.api_keys
		WHERE key_id = $1 AND deleted_at IS NULL
	`

	return r.scanApiKey(ctx, query, keyID)
}

// FindAllByUserAndCompany finds all API keys for a user in a company
func (r *ApiKeyRepository) FindAllByUserAndCompany(
	ctx context.Context,
	userID, companyID string,
	limit, offset int,
	environment *string,
	isActive *bool,
) ([]domain.ApiKey, int64, error) {
	// Count query
	countQuery := `
		SELECT COUNT(*)
		FROM core.api_keys
		WHERE user_id = $1 AND company_id = $2 AND deleted_at IS NULL
	`
	args := []interface{}{userID, companyID}
	argIndex := 3

	if environment != nil {
		countQuery += ` AND environment = $` + string(rune('0'+argIndex))
		args = append(args, *environment)
		argIndex++
	}

	if isActive != nil {
		if *isActive {
			countQuery += ` AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())`
		} else {
			countQuery += ` AND (revoked_at IS NOT NULL OR (expires_at IS NOT NULL AND expires_at <= NOW()))`
		}
	}

	var total int64
	err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		logger.Error("Failed to count API keys", logger.Err(err))
		return nil, 0, err
	}

	// Data query
	dataQuery := `
		SELECT id, user_id, company_id, key_id, secret_hash, key_prefix,
		       name, description, environment, scoped_permissions, ip_whitelist,
		       rate_limit, rate_limit_window, expires_at, revoked_at, revoked_by,
		       revoke_reason, last_used_at, last_used_ip, total_requests,
		       created_at, created_by, updated_at, updated_by
		FROM core.api_keys
		WHERE user_id = $1 AND company_id = $2 AND deleted_at IS NULL
	`

	args = []interface{}{userID, companyID}
	argIndex = 3

	if environment != nil {
		dataQuery += ` AND environment = $` + string(rune('0'+argIndex))
		args = append(args, *environment)
		argIndex++
	}

	if isActive != nil {
		if *isActive {
			dataQuery += ` AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())`
		} else {
			dataQuery += ` AND (revoked_at IS NOT NULL OR (expires_at IS NOT NULL AND expires_at <= NOW()))`
		}
	}

	dataQuery += ` ORDER BY created_at DESC LIMIT $` + string(rune('0'+argIndex)) + ` OFFSET $` + string(rune('0'+argIndex+1))
	args = append(args, limit, offset)

	rows, err := r.db.Query(ctx, dataQuery, args...)
	if err != nil {
		logger.Error("Failed to find API keys", logger.Err(err))
		return nil, 0, err
	}
	defer rows.Close()

	var apiKeys []domain.ApiKey
	for rows.Next() {
		apiKey, err := r.scanRow(rows)
		if err != nil {
			return nil, 0, err
		}
		apiKeys = append(apiKeys, *apiKey)
	}

	return apiKeys, total, nil
}

// Update updates an API key
func (r *ApiKeyRepository) Update(ctx context.Context, apiKey *domain.ApiKey) error {
	query := `
		UPDATE core.api_keys
		SET name = $3, description = $4, scoped_permissions = $5, ip_whitelist = $6,
		    rate_limit = $7, rate_limit_window = $8, updated_at = $9, updated_by = $10
		WHERE id = $1 AND company_id = $2 AND deleted_at IS NULL
	`

	result, err := r.db.Exec(ctx, query,
		apiKey.ID, apiKey.CompanyID,
		apiKey.Name, apiKey.Description, apiKey.ScopedPermissions, apiKey.IPWhitelist,
		apiKey.RateLimit, apiKey.RateLimitWindow, apiKey.UpdatedAt, apiKey.UpdatedBy,
	)

	if err != nil {
		logger.Error("Failed to update API key", logger.Err(err))
		return err
	}

	if result.RowsAffected() == 0 {
		return errors.New("api key not found")
	}

	return nil
}

// Revoke revokes an API key
func (r *ApiKeyRepository) Revoke(ctx context.Context, id, companyID, revokedBy string, reason *string) error {
	query := `
		UPDATE core.api_keys
		SET revoked_at = $3, revoked_by = $4, revoke_reason = $5, updated_at = $3, updated_by = $4
		WHERE id = $1 AND company_id = $2 AND deleted_at IS NULL AND revoked_at IS NULL
	`

	now := time.Now()
	result, err := r.db.Exec(ctx, query, id, companyID, now, revokedBy, reason)

	if err != nil {
		logger.Error("Failed to revoke API key", logger.Err(err))
		return err
	}

	if result.RowsAffected() == 0 {
		return errors.New("api key not found or already revoked")
	}

	return nil
}

// Delete soft deletes an API key
func (r *ApiKeyRepository) Delete(ctx context.Context, id, companyID, deletedBy string) error {
	query := `
		UPDATE core.api_keys
		SET deleted_at = $3, deleted_by = $4
		WHERE id = $1 AND company_id = $2 AND deleted_at IS NULL
	`

	now := time.Now()
	result, err := r.db.Exec(ctx, query, id, companyID, now, deletedBy)

	if err != nil {
		logger.Error("Failed to delete API key", logger.Err(err))
		return err
	}

	if result.RowsAffected() == 0 {
		return errors.New("api key not found")
	}

	return nil
}

// UpdateUsage updates the usage statistics for an API key
func (r *ApiKeyRepository) UpdateUsage(ctx context.Context, keyID, clientIP string) error {
	query := `
		UPDATE core.api_keys
		SET last_used_at = $2, last_used_ip = $3, total_requests = total_requests + 1
		WHERE key_id = $1 AND deleted_at IS NULL
	`

	_, err := r.db.Exec(ctx, query, keyID, time.Now(), clientIP)
	if err != nil {
		logger.Error("Failed to update API key usage", logger.Err(err))
		return err
	}

	return nil
}

// scanApiKey scans a single API key row
func (r *ApiKeyRepository) scanApiKey(ctx context.Context, query string, args ...interface{}) (*domain.ApiKey, error) {
	row := r.db.QueryRow(ctx, query, args...)

	apiKey, err := r.scanSingleRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		logger.Error("Failed to scan API key", logger.Err(err))
		return nil, err
	}

	return apiKey, nil
}

// scanSingleRow scans a single row into an ApiKey
func (r *ApiKeyRepository) scanSingleRow(row pgx.Row) (*domain.ApiKey, error) {
	var apiKey domain.ApiKey
	var environment string

	err := row.Scan(
		&apiKey.ID, &apiKey.UserID, &apiKey.CompanyID, &apiKey.KeyID, &apiKey.SecretHash, &apiKey.KeyPrefix,
		&apiKey.Name, &apiKey.Description, &environment, &apiKey.ScopedPermissions, &apiKey.IPWhitelist,
		&apiKey.RateLimit, &apiKey.RateLimitWindow, &apiKey.ExpiresAt, &apiKey.RevokedAt, &apiKey.RevokedBy,
		&apiKey.RevokeReason, &apiKey.LastUsedAt, &apiKey.LastUsedIP, &apiKey.TotalRequests,
		&apiKey.CreatedAt, &apiKey.CreatedBy, &apiKey.UpdatedAt, &apiKey.UpdatedBy,
	)

	if err != nil {
		return nil, err
	}

	apiKey.Environment = domain.ApiKeyEnvironment(environment)
	return &apiKey, nil
}

// scanRow scans a row from rows iterator into an ApiKey
func (r *ApiKeyRepository) scanRow(rows pgx.Rows) (*domain.ApiKey, error) {
	var apiKey domain.ApiKey
	var environment string

	err := rows.Scan(
		&apiKey.ID, &apiKey.UserID, &apiKey.CompanyID, &apiKey.KeyID, &apiKey.SecretHash, &apiKey.KeyPrefix,
		&apiKey.Name, &apiKey.Description, &environment, &apiKey.ScopedPermissions, &apiKey.IPWhitelist,
		&apiKey.RateLimit, &apiKey.RateLimitWindow, &apiKey.ExpiresAt, &apiKey.RevokedAt, &apiKey.RevokedBy,
		&apiKey.RevokeReason, &apiKey.LastUsedAt, &apiKey.LastUsedIP, &apiKey.TotalRequests,
		&apiKey.CreatedAt, &apiKey.CreatedBy, &apiKey.UpdatedAt, &apiKey.UpdatedBy,
	)

	if err != nil {
		logger.Error("Failed to scan API key row", logger.Err(err))
		return nil, err
	}

	apiKey.Environment = domain.ApiKeyEnvironment(environment)
	return &apiKey, nil
}
