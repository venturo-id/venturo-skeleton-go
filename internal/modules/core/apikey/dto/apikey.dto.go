package dto

import "time"

// CreateAPIKeyRequest represents the request body for creating a new API key.
// ScopedPermissions must list every permission the key may use; each one
// must be held by the creator.
type CreateAPIKeyRequest struct {
	Name              string   `json:"name" binding:"required,min=1,max=100"`
	Description       *string  `json:"description" binding:"omitempty,max=500"`
	Environment       string   `json:"environment" binding:"omitempty,oneof=live test"`
	ScopedPermissions []string `json:"scoped_permissions" binding:"required,min=1,max=200,dive,required,max=100"`
	IPWhitelist       []string `json:"ip_whitelist" binding:"omitempty,max=50,dive,ip|cidr"`
	RateLimit         *int     `json:"rate_limit" binding:"omitempty,min=1,max=100000"`
	RateLimitWindow   *int     `json:"rate_limit_window" binding:"omitempty,min=60,max=86400"`
	ExpiresAt         *string  `json:"expires_at" binding:"omitempty,datetime=2006-01-02T15:04:05Z07:00"` // RFC3339
}

// CreateAPIKeyResponse represents the response after creating an API key
// The full key is returned ONLY once during creation
type CreateAPIKeyResponse struct {
	ID        string `json:"id"`
	Key       string `json:"key"`        // Full key: tbx_<env>_<key_id>_<secret> - shown ONCE
	KeyPrefix string `json:"key_prefix"` // For future reference
	Name      string `json:"name"`
	Message   string `json:"message"`
}

// UpdateAPIKeyRequest represents the request body for updating an API key.
// A ScopedPermissions value replaces the whole scope and may not be empty.
type UpdateAPIKeyRequest struct {
	Name              *string  `json:"name" binding:"omitempty,min=1,max=100"`
	Description       *string  `json:"description" binding:"omitempty,max=500"`
	ScopedPermissions []string `json:"scoped_permissions" binding:"omitempty,max=200,dive,required,max=100"`
	IPWhitelist       []string `json:"ip_whitelist" binding:"omitempty,max=50,dive,ip|cidr"`
	RateLimit         *int     `json:"rate_limit" binding:"omitempty,min=1,max=100000"`
	RateLimitWindow   *int     `json:"rate_limit_window" binding:"omitempty,min=60,max=86400"`
}

// RevokeAPIKeyRequest represents the request body for revoking an API key
type RevokeAPIKeyRequest struct {
	Reason *string `json:"reason" binding:"omitempty,max=500"`
}

// APIKeyResponse represents an API key in responses (masked)
type APIKeyResponse struct {
	ID                string     `json:"id"`
	KeyPrefix         string     `json:"key_prefix"`
	Name              string     `json:"name"`
	Description       *string    `json:"description,omitempty"`
	Environment       string     `json:"environment"`
	ScopedPermissions []string   `json:"scoped_permissions"`
	IPWhitelist       []string   `json:"ip_whitelist,omitempty"`
	RateLimit         int        `json:"rate_limit"`
	RateLimitWindow   int        `json:"rate_limit_window"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt        *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP        *string    `json:"last_used_ip,omitempty"`
	TotalRequests     int64      `json:"total_requests"`
	IsActive          bool       `json:"is_active"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// APIKeyQueryParams represents query parameters for listing API keys
type APIKeyQueryParams struct {
	Page        int     `form:"page,default=1" binding:"min=1,max=100000"`
	Limit       int     `form:"limit,default=10" binding:"min=1,max=100"`
	Environment *string `form:"environment" binding:"omitempty,oneof=live test"`
	IsActive    *bool   `form:"is_active"`
}
