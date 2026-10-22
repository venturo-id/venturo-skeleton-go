package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"

	"venturo-skeleton-go-simple/internal/modules/core/api_key/domain"
	"venturo-skeleton-go-simple/internal/modules/core/api_key/dto"
	"venturo-skeleton-go-simple/internal/modules/core/api_key/repository"
	"venturo-skeleton-go-simple/pkg/jwt"
	"venturo-skeleton-go-simple/pkg/logger"
)

// Service errors
var (
	ErrApiKeyNotFound      = errors.New("API key not found")
	ErrApiKeyRevoked       = errors.New("API key has been revoked")
	ErrApiKeyExpired       = errors.New("API key has expired")
	ErrApiKeyIPNotAllowed  = errors.New("IP address not allowed for this API key")
	ErrApiKeyInvalidSecret = errors.New("invalid API key secret")
	ErrApiKeyInvalidFormat = errors.New("invalid API key format")
	ErrApiKeyInvalidScope  = errors.New("invalid permission scope")
	ErrApiKeyUserInactive  = errors.New("API key owner account is inactive")
)

// RoleRepository interface for getting user permissions
type RoleRepository interface {
	GetUserPermissions(ctx context.Context, userID string) ([]string, error)
	GetUserRoleNames(ctx context.Context, userID string) ([]string, error)
}

// UserRepository interface for getting user info
type UserRepository interface {
	FindByID(ctx context.Context, id string) (interface{}, error)
}

type ApiKeyService struct {
	apiKeyRepo *repository.ApiKeyRepository
	roleRepo   RoleRepository
}

func NewApiKeyService(apiKeyRepo *repository.ApiKeyRepository, roleRepo RoleRepository) *ApiKeyService {
	return &ApiKeyService{
		apiKeyRepo: apiKeyRepo,
		roleRepo:   roleRepo,
	}
}

// Create creates a new API key
func (s *ApiKeyService) Create(
	ctx context.Context,
	req *dto.CreateApiKeyRequest,
	userID, createdBy string,
) (*dto.CreateApiKeyResponse, error) {
	// Set defaults
	environment := domain.EnvLive
	if req.Environment != "" {
		environment = domain.ApiKeyEnvironment(req.Environment)
	}

	rateLimit := 1000
	if req.RateLimit != nil {
		rateLimit = *req.RateLimit
	}

	rateLimitWindow := 3600
	if req.RateLimitWindow != nil {
		rateLimitWindow = *req.RateLimitWindow
	}

	// Parse expiration
	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("invalid expires_at format, expected RFC3339: %w", err)
		}
		expiresAt = &parsed
	}

	// Validate scoped permissions
	if len(req.ScopedPermissions) > 0 {
		if err := s.validateScopes(ctx, userID, req.ScopedPermissions); err != nil {
			return nil, err
		}
	}

	// Generate key components
	keyID, err := generateKeyID(8)
	if err != nil {
		logger.Error("Failed to generate key ID", logger.Err(err))
		return nil, err
	}

	secret, err := generateSecret(32)
	if err != nil {
		logger.Error("Failed to generate secret", logger.Err(err))
		return nil, err
	}

	// Compose full key
	fullKey := fmt.Sprintf("wiz_%s_%s_%s", environment, keyID, secret)
	keyPrefix := fmt.Sprintf("wiz_%s_%s_%s...", environment, keyID, secret[:8])

	// Hash the secret
	secretHash := hashSecret(secret)

	now := time.Now()
	apiKey := &domain.ApiKey{
		ID:                uuid.New().String(),
		UserID:            userID,
		KeyID:             keyID,
		SecretHash:        secretHash,
		KeyPrefix:         keyPrefix,
		Name:              req.Name,
		Description:       req.Description,
		Environment:       environment,
		ScopedPermissions: req.ScopedPermissions,
		IPWhitelist:       req.IPWhitelist,
		RateLimit:         rateLimit,
		RateLimitWindow:   rateLimitWindow,
		ExpiresAt:         expiresAt,
		TotalRequests:     0,
		CreatedAt:         now,
		CreatedBy:         &createdBy,
		UpdatedAt:         now,
		UpdatedBy:         &createdBy,
	}

	if err := s.apiKeyRepo.Create(ctx, apiKey); err != nil {
		return nil, err
	}

	logger.Info("API key created",
		logger.String("api_key_id", apiKey.ID),
		logger.String("user_id", userID),
	)

	return &dto.CreateApiKeyResponse{
		ID:        apiKey.ID,
		Key:       fullKey,
		KeyPrefix: keyPrefix,
		Name:      apiKey.Name,
		Message:   "API key created successfully. Save this key securely - it will not be shown again.",
	}, nil
}

// GetByID gets an API key by ID, restricted to keys owned by userID
func (s *ApiKeyService) GetByID(ctx context.Context, id, userID string) (*dto.ApiKeyResponse, error) {
	apiKey, err := s.apiKeyRepo.FindByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	if apiKey == nil {
		return nil, ErrApiKeyNotFound
	}

	return s.toResponse(apiKey), nil
}

// List lists all API keys owned by a user
func (s *ApiKeyService) List(
	ctx context.Context,
	userID string,
	params *dto.ApiKeyQueryParams,
) (*dto.ApiKeyListResponse, error) {
	// Set defaults
	page := 1
	pageSize := 20
	if params.Page > 0 {
		page = params.Page
	}
	if params.Limit > 0 {
		pageSize = params.Limit
	}

	offset := (page - 1) * pageSize

	apiKeys, total, err := s.apiKeyRepo.FindAllByUser(
		ctx, userID, pageSize, offset, params.Environment, params.IsActive,
	)
	if err != nil {
		return nil, err
	}

	responses := make([]dto.ApiKeyResponse, len(apiKeys))
	for i, apiKey := range apiKeys {
		responses[i] = *s.toResponse(&apiKey)
	}

	return &dto.ApiKeyListResponse{
		ApiKeys: responses,
		Total:   total,
		Page:    page,
		Limit:   pageSize,
	}, nil
}

// Update updates an API key
func (s *ApiKeyService) Update(
	ctx context.Context,
	id, userID string,
	req *dto.UpdateApiKeyRequest,
) (*dto.ApiKeyResponse, error) {
	apiKey, err := s.apiKeyRepo.FindByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	if apiKey == nil {
		return nil, ErrApiKeyNotFound
	}

	// Validate scoped permissions if provided
	if len(req.ScopedPermissions) > 0 {
		if err := s.validateScopes(ctx, userID, req.ScopedPermissions); err != nil {
			return nil, err
		}
	}

	// Apply updates
	if req.Name != nil {
		apiKey.Name = *req.Name
	}
	if req.Description != nil {
		apiKey.Description = req.Description
	}
	if req.ScopedPermissions != nil {
		apiKey.ScopedPermissions = req.ScopedPermissions
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
		return nil, err
	}

	logger.Info("API key updated",
		logger.String("api_key_id", apiKey.ID),
		logger.String("user_id", userID),
	)

	return s.toResponse(apiKey), nil
}

// Revoke revokes an API key
func (s *ApiKeyService) Revoke(
	ctx context.Context,
	id, userID string,
	req *dto.RevokeApiKeyRequest,
) error {
	apiKey, err := s.apiKeyRepo.FindByID(ctx, id, userID)
	if err != nil {
		return err
	}
	if apiKey == nil {
		return ErrApiKeyNotFound
	}

	if err := s.apiKeyRepo.Revoke(ctx, id, userID, userID, req.Reason); err != nil {
		return err
	}

	logger.Info("API key revoked",
		logger.String("api_key_id", id),
		logger.String("user_id", userID),
	)

	return nil
}

// ValidateAndGetClaims validates an API key and returns JWT-compatible claims
func (s *ApiKeyService) ValidateAndGetClaims(
	ctx context.Context,
	keyID, secret, clientIP string,
) (*jwt.Claims, error) {
	// Find API key by key_id
	apiKey, err := s.apiKeyRepo.FindByKeyID(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if apiKey == nil {
		return nil, ErrApiKeyNotFound
	}

	// Verify secret
	secretHash := hashSecret(secret)
	if secretHash != apiKey.SecretHash {
		return nil, ErrApiKeyInvalidSecret
	}

	// Check if revoked
	if apiKey.IsRevoked() {
		return nil, ErrApiKeyRevoked
	}

	// Check if expired
	if apiKey.IsExpired() {
		return nil, ErrApiKeyExpired
	}

	// Check IP whitelist
	if !apiKey.IsIPAllowed(clientIP) {
		return nil, ErrApiKeyIPNotAllowed
	}

	// Update usage (async, don't block on errors)
	go func() {
		_ = s.apiKeyRepo.UpdateUsage(context.Background(), keyID, clientIP)
	}()

	// Get user permissions
	var roles, permissions []string
	if s.roleRepo != nil {
		roles, _ = s.roleRepo.GetUserRoleNames(ctx, apiKey.UserID)
		userPerms, _ := s.roleRepo.GetUserPermissions(ctx, apiKey.UserID)
		permissions = apiKey.GetEffectivePermissions(userPerms)
	}

	// Build JWT-compatible claims. API keys may carry a scope narrower
	// than the user's full permission set, so the effective permissions
	// are pinned onto ScopedPermissions (runtime-only) instead of going
	// through the Redis cache that RequirePermission uses for JWTs.
	claims := &jwt.Claims{
		UserID:            apiKey.UserID,
		Roles:             roles,
		ScopedPermissions: permissions,
	}

	return claims, nil
}

// validateScopes validates that the requested scopes are a subset of user's permissions
func (s *ApiKeyService) validateScopes(ctx context.Context, userID string, scopes []string) error {
	if s.roleRepo == nil {
		return nil
	}

	userPerms, err := s.roleRepo.GetUserPermissions(ctx, userID)
	if err != nil {
		return err
	}

	userPermSet := make(map[string]bool)
	for _, p := range userPerms {
		userPermSet[p] = true
	}

	for _, scope := range scopes {
		if !userPermSet[scope] {
			return fmt.Errorf("%w: permission '%s' not available to user", ErrApiKeyInvalidScope, scope)
		}
	}

	return nil
}

// toResponse converts a domain.ApiKey to dto.ApiKeyResponse
func (s *ApiKeyService) toResponse(apiKey *domain.ApiKey) *dto.ApiKeyResponse {
	isActive := apiKey.IsValid()

	return &dto.ApiKeyResponse{
		ID:                apiKey.ID,
		KeyPrefix:         apiKey.KeyPrefix,
		Name:              apiKey.Name,
		Description:       apiKey.Description,
		Environment:       string(apiKey.Environment),
		ScopedPermissions: apiKey.ScopedPermissions,
		IPWhitelist:       apiKey.IPWhitelist,
		RateLimit:         apiKey.RateLimit,
		RateLimitWindow:   apiKey.RateLimitWindow,
		ExpiresAt:         apiKey.ExpiresAt,
		RevokedAt:         apiKey.RevokedAt,
		LastUsedAt:        apiKey.LastUsedAt,
		LastUsedIP:        apiKey.LastUsedIP,
		TotalRequests:     apiKey.TotalRequests,
		IsActive:          isActive,
		CreatedAt:         apiKey.CreatedAt,
		UpdatedAt:         apiKey.UpdatedAt,
	}
}

// generateKeyID generates a random alphanumeric key ID
func generateKeyID(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, length)

	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		result[i] = charset[n.Int64()]
	}

	return string(result), nil
}

// generateSecret generates a random hex secret
func generateSecret(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// hashSecret creates a SHA256 hash of the secret
func hashSecret(secret string) string {
	h := sha256.New()
	h.Write([]byte(secret))
	return hex.EncodeToString(h.Sum(nil))
}
