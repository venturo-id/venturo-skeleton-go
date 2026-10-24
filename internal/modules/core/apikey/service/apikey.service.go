package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"

	"timebox-be/internal/database"
	"timebox-be/internal/modules/core/apikey/domain"
	"timebox-be/internal/modules/core/apikey/dto"
	"timebox-be/internal/modules/core/apikey/repository"
	roleDomain "timebox-be/internal/modules/core/role/domain"
	"timebox-be/internal/shared/audit"
	"timebox-be/internal/shared/authz"
	"timebox-be/internal/shared/ratelimit"
	"timebox-be/pkg/jwt"
	"timebox-be/pkg/logger"
	"timebox-be/pkg/token"
)

// Service errors
var (
	ErrAPIKeyNotFound       = errors.New("API key not found")
	ErrAPIKeyRevoked        = errors.New("API key has been revoked")
	ErrAPIKeyAlreadyRevoked = errors.New("API key is already revoked")
	ErrAPIKeyExpired        = errors.New("API key has expired")
	ErrAPIKeyIPNotAllowed   = errors.New("IP address not allowed for this API key")
	ErrAPIKeyInvalidSecret  = errors.New("invalid API key secret")
	ErrAPIKeyInvalidScope   = errors.New("invalid permission scope")
	ErrInvalidExpiry        = errors.New("expires_at must be in the future")
)

// Defaults for a key created without its own limit.
const (
	defaultRateLimit       = 1000
	defaultRateLimitWindow = 3600 // seconds
)

// SubjectReader returns a user's live authorization state (implemented by
// *authz.Service).
type SubjectReader interface {
	GetSubject(ctx context.Context, userID string) (authz.Subject, error)
}

// RateLimiter enforces each key's rate_limit per rate_limit_window
// (implemented by *ratelimit.Limiter).
type RateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
}

type APIKeyService struct {
	apiKeyRepo *repository.APIKeyRepository
	subjects   SubjectReader
	limiter    RateLimiter
	audit      audit.Recorder
}

// NewAPIKeyService wires the API key service; every dependency is required.
func NewAPIKeyService(apiKeyRepo *repository.APIKeyRepository, subjects SubjectReader, limiter RateLimiter, recorder audit.Recorder) *APIKeyService {
	return &APIKeyService{
		apiKeyRepo: apiKeyRepo,
		subjects:   subjects,
		limiter:    limiter,
		audit:      recorder,
	}
}

// record writes an audit entry about apiKey.
func (s *APIKeyService) record(ctx context.Context, action string, apiKey *domain.APIKey) {
	s.audit.Record(ctx, audit.Entry{
		Action: action, TargetType: audit.TargetAPIKey, TargetID: apiKey.ID,
		Details: map[string]any{"key_id": apiKey.KeyID, "environment": string(apiKey.Environment), "scoped_permissions": apiKey.ScopedPermissions},
	})
}

// Create creates a new API key for userID. The full key is returned once
// and never stored.
func (s *APIKeyService) Create(ctx context.Context, req *dto.CreateAPIKeyRequest, userID string) (*dto.CreateAPIKeyResponse, error) {
	environment := domain.EnvLive
	if req.Environment != "" {
		environment = domain.APIKeyEnvironment(req.Environment)
	}

	rateLimit := defaultRateLimit
	if req.RateLimit != nil {
		rateLimit = *req.RateLimit
	}

	rateLimitWindow := defaultRateLimitWindow
	if req.RateLimitWindow != nil {
		rateLimitWindow = *req.RateLimitWindow
	}

	expiresAt, err := parseExpiry(req.ExpiresAt)
	if err != nil {
		return nil, err
	}

	scopes, err := s.validateScopes(ctx, userID, req.ScopedPermissions)
	if err != nil {
		return nil, err
	}

	keyID, err := generateKeyID(8)
	if err != nil {
		logger.Error("Failed to generate key ID", logger.Err(err))
		return nil, err
	}

	secret, err := token.GenerateSecureToken(32)
	if err != nil {
		logger.Error("Failed to generate secret", logger.Err(err))
		return nil, err
	}

	fullKey := fmt.Sprintf("%s_%s_%s_%s", authz.APIKeyPrefix, environment, keyID, secret)
	keyPrefix := fmt.Sprintf("%s_%s_%s_%s...", authz.APIKeyPrefix, environment, keyID, secret[:8])

	now := time.Now()
	apiKey := &domain.APIKey{
		ID:                uuid.New().String(),
		UserID:            userID,
		KeyID:             keyID,
		SecretHash:        token.Hash(secret),
		KeyPrefix:         keyPrefix,
		Name:              req.Name,
		Description:       req.Description,
		Environment:       environment,
		ScopedPermissions: scopes,
		IPWhitelist:       req.IPWhitelist,
		RateLimit:         rateLimit,
		RateLimitWindow:   rateLimitWindow,
		ExpiresAt:         expiresAt,
		CreatedAt:         now,
		CreatedBy:         &userID,
		UpdatedAt:         now,
		UpdatedBy:         &userID,
	}

	if err := s.apiKeyRepo.Create(ctx, apiKey); err != nil {
		return nil, err
	}

	logger.Info("API key created",
		logger.String("api_key_id", apiKey.ID),
		logger.String("user_id", userID),
	)
	s.record(ctx, audit.ActionAPIKeyCreated, apiKey)

	return &dto.CreateAPIKeyResponse{
		ID:        apiKey.ID,
		Key:       fullKey,
		KeyPrefix: keyPrefix,
		Name:      apiKey.Name,
		Message:   "API key created successfully. Save this key securely - it will not be shown again.",
	}, nil
}

// GetByID gets an API key by ID, restricted to keys owned by userID
func (s *APIKeyService) GetByID(ctx context.Context, id, userID string) (*dto.APIKeyResponse, error) {
	apiKey, err := s.apiKeyRepo.FindByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	if apiKey == nil {
		return nil, ErrAPIKeyNotFound
	}

	response := toResponse(apiKey)
	return &response, nil
}

// List returns one page of the keys owned by userID, and the total.
func (s *APIKeyService) List(ctx context.Context, userID string, params *dto.APIKeyQueryParams) ([]dto.APIKeyResponse, int64, error) {
	apiKeys, total, err := s.apiKeyRepo.FindAllByUser(ctx, userID, params)
	if err != nil {
		return nil, 0, err
	}

	responses := make([]dto.APIKeyResponse, len(apiKeys))
	for i := range apiKeys {
		responses[i] = toResponse(&apiKeys[i])
	}

	return responses, total, nil
}

// Update updates an API key owned by userID.
func (s *APIKeyService) Update(ctx context.Context, id, userID string, req *dto.UpdateAPIKeyRequest) (*dto.APIKeyResponse, error) {
	apiKey, err := s.apiKeyRepo.FindByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	if apiKey == nil {
		return nil, ErrAPIKeyNotFound
	}

	if req.ScopedPermissions != nil {
		scopes, err := s.validateScopes(ctx, userID, req.ScopedPermissions)
		if err != nil {
			return nil, err
		}
		apiKey.ScopedPermissions = scopes
	}
	if req.Name != nil {
		apiKey.Name = *req.Name
	}
	if req.Description != nil {
		apiKey.Description = req.Description
	}
	if req.IPWhitelist != nil {
		apiKey.IPWhitelist = req.IPWhitelist
	}
	if req.RateLimit != nil {
		apiKey.RateLimit = *req.RateLimit
	}
	if req.RateLimitWindow != nil {
		apiKey.RateLimitWindow = *req.RateLimitWindow
	}

	apiKey.UpdatedAt = time.Now()
	apiKey.UpdatedBy = &userID

	if err := s.apiKeyRepo.Update(ctx, apiKey); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return nil, ErrAPIKeyNotFound
		}
		return nil, err
	}

	logger.Info("API key updated",
		logger.String("api_key_id", apiKey.ID),
		logger.String("user_id", userID),
	)
	s.record(ctx, audit.ActionAPIKeyUpdated, apiKey)

	response := toResponse(apiKey)
	return &response, nil
}

// Revoke revokes an API key owned by userID.
func (s *APIKeyService) Revoke(ctx context.Context, id, userID string, req *dto.RevokeAPIKeyRequest) error {
	apiKey, err := s.apiKeyRepo.FindByID(ctx, id, userID)
	if err != nil {
		return err
	}
	if apiKey == nil {
		return ErrAPIKeyNotFound
	}
	if apiKey.IsRevoked() {
		return ErrAPIKeyAlreadyRevoked
	}

	if err := s.apiKeyRepo.Revoke(ctx, id, userID, userID, req.Reason); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			// Revoked by a concurrent request.
			return ErrAPIKeyAlreadyRevoked
		}
		return err
	}

	logger.Info("API key revoked",
		logger.String("api_key_id", id),
		logger.String("user_id", userID),
	)
	s.record(ctx, audit.ActionAPIKeyRevoked, apiKey)

	return nil
}

// RevokeAllForUser revokes every active key of userID, e.g. when the
// account turns out to have been controlled by someone else.
func (s *APIKeyService) RevokeAllForUser(ctx context.Context, userID, reason string) error {
	revoked, err := s.apiKeyRepo.RevokeAllByUser(ctx, userID, reason)
	if err != nil {
		return err
	}
	if revoked > 0 {
		logger.Warn("API keys revoked",
			logger.String("user_id", userID),
			logger.Int64("count", revoked),
			logger.String("reason", reason))
	}
	return nil
}

// ListAllForUser returns every key of userID, revoked ones included —
// for the account data export.
func (s *APIKeyService) ListAllForUser(ctx context.Context, userID string) ([]dto.APIKeyResponse, error) {
	apiKeys, err := s.apiKeyRepo.ListAllByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	responses := make([]dto.APIKeyResponse, len(apiKeys))
	for i := range apiKeys {
		responses[i] = toResponse(&apiKeys[i])
	}
	return responses, nil
}

// DeleteAllForUser deletes every key of userID, for a closed account. It
// joins the transaction carried by ctx.
func (s *APIKeyService) DeleteAllForUser(ctx context.Context, userID string) error {
	return s.apiKeyRepo.DeleteAllByUser(ctx, userID)
}

// ValidateAndGetClaims validates an API key and returns claims naming its
// owner, with the key's stored scope in ScopedPermissions. The caller
// narrows that scope to what the owner still holds.
//
// A rejected key comes back as *authz.CredentialError. An unknown key_id,
// a wrong secret and a wrong environment share one reason, so a caller
// cannot probe which key_ids exist. Reasons for revoked / expired /
// IP-restricted keys are only given once the secret has checked out.
func (s *APIKeyService) ValidateAndGetClaims(ctx context.Context, environment, keyID, secret, clientIP string) (*jwt.Claims, error) {
	apiKey, err := s.apiKeyRepo.FindByKeyID(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if apiKey == nil {
		return nil, rejectKey("Invalid API key", ErrAPIKeyNotFound)
	}

	if subtle.ConstantTimeCompare([]byte(token.Hash(secret)), []byte(apiKey.SecretHash)) != 1 ||
		environment != string(apiKey.Environment) {
		return nil, rejectKey("Invalid API key", ErrAPIKeyInvalidSecret)
	}

	if apiKey.IsRevoked() {
		return nil, rejectKey("API key has been revoked", ErrAPIKeyRevoked)
	}
	if apiKey.IsExpired() {
		return nil, rejectKey("API key has expired", ErrAPIKeyExpired)
	}
	if !apiKey.IsIPAllowed(clientIP) {
		return nil, rejectKey("IP address not allowed for this API key", ErrAPIKeyIPNotAllowed)
	}

	// Enforce the key's own rate limit. Counted only after the secret
	// checks out, so nobody can burn a key's quota without holding it.
	window := time.Duration(apiKey.RateLimitWindow) * time.Second
	allowed, retryAfter, err := s.limiter.Allow(ctx, "apikey:"+apiKey.KeyID, apiKey.RateLimit, window)
	if err != nil {
		logger.Warn("API key rate limiter unavailable, allowing request",
			logger.String("key_id", apiKey.KeyID),
			logger.Err(err))
	}
	if !allowed {
		return nil, &ratelimit.ExceededError{RetryAfter: retryAfter}
	}

	// Usage stats are best effort and must not slow the request down.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.apiKeyRepo.UpdateUsage(ctx, keyID, clientIP)
	}()

	return &jwt.Claims{
		UserID:            apiKey.UserID,
		ScopedPermissions: append([]string(nil), apiKey.ScopedPermissions...),
	}, nil
}

// rejectKey wraps a validation failure with the reason the client may see.
func rejectKey(reason string, err error) error {
	return &authz.CredentialError{Reason: reason, Err: err}
}

// validateScopes checks that scopes is a non-empty list of real
// permission codes the owner holds, and returns it without duplicates.
func (s *APIKeyService) validateScopes(ctx context.Context, userID string, scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return nil, fmt.Errorf("%w: list at least one permission", ErrAPIKeyInvalidScope)
	}

	owner, err := s.subjects.GetSubject(ctx, userID)
	if err != nil {
		return nil, err
	}

	return checkScopes(scopes, owner)
}

// checkScopes is validateScopes once the owner's subject is known.
func checkScopes(scopes []string, owner authz.Subject) ([]string, error) {
	seen := make(map[string]struct{}, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		resource, action, ok := authz.SplitPermission(scope)
		if !ok || !authz.IsResource(resource) || !roleDomain.IsValidAction(action) {
			return nil, fmt.Errorf("%w: %q is not a permission", ErrAPIKeyInvalidScope, scope)
		}
		if !owner.Has(scope) {
			return nil, fmt.Errorf("%w: you do not hold %q", ErrAPIKeyInvalidScope, scope)
		}
		if _, dup := seen[scope]; !dup {
			seen[scope] = struct{}{}
			out = append(out, scope)
		}
	}
	return out, nil
}

// parseExpiry reads an optional RFC 3339 expiry, which must lie ahead.
func parseExpiry(value *string) (*time.Time, error) {
	if value == nil || *value == "" {
		return nil, nil
	}
	expiresAt, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return nil, fmt.Errorf("%w: expected RFC 3339", ErrInvalidExpiry)
	}
	if !expiresAt.After(time.Now()) {
		return nil, ErrInvalidExpiry
	}
	return &expiresAt, nil
}

// toResponse converts a domain.APIKey to dto.APIKeyResponse
func toResponse(apiKey *domain.APIKey) dto.APIKeyResponse {
	scopes := apiKey.ScopedPermissions
	if scopes == nil {
		scopes = []string{}
	}
	return dto.APIKeyResponse{
		ID:                apiKey.ID,
		KeyPrefix:         apiKey.KeyPrefix,
		Name:              apiKey.Name,
		Description:       apiKey.Description,
		Environment:       string(apiKey.Environment),
		ScopedPermissions: scopes,
		IPWhitelist:       apiKey.IPWhitelist,
		RateLimit:         apiKey.RateLimit,
		RateLimitWindow:   apiKey.RateLimitWindow,
		ExpiresAt:         apiKey.ExpiresAt,
		RevokedAt:         apiKey.RevokedAt,
		LastUsedAt:        apiKey.LastUsedAt,
		LastUsedIP:        apiKey.LastUsedIP,
		TotalRequests:     apiKey.TotalRequests,
		IsActive:          apiKey.IsValid(),
		CreatedAt:         apiKey.CreatedAt,
		UpdatedAt:         apiKey.UpdatedAt,
	}
}

// generateKeyID generates a random lowercase alphanumeric key ID. It has
// no underscore, the separator of the full key.
func generateKeyID(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, length)

	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		result[i] = charset[n.Int64()]
	}

	return string(result), nil
}
