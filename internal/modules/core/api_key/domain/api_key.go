package domain

import (
	"net"
	"strings"
	"time"
)

// ApiKeyEnvironment represents the environment type for an API key
type ApiKeyEnvironment string

const (
	EnvLive ApiKeyEnvironment = "live"
	EnvTest ApiKeyEnvironment = "test"
)

// ApiKey represents an API key entity
type ApiKey struct {
	ID                string            `json:"id"`
	UserID            string            `json:"user_id"`
	KeyID             string            `json:"key_id"`
	SecretHash        string            `json:"-"` // Never expose in JSON
	KeyPrefix         string            `json:"key_prefix"`
	Name              string            `json:"name"`
	Description       *string           `json:"description,omitempty"`
	Environment       ApiKeyEnvironment `json:"environment"`
	ScopedPermissions []string          `json:"scoped_permissions"`
	IPWhitelist       []string          `json:"ip_whitelist,omitempty"`
	RateLimit         int               `json:"rate_limit"`
	RateLimitWindow   int               `json:"rate_limit_window"`
	ExpiresAt         *time.Time        `json:"expires_at,omitempty"`
	RevokedAt         *time.Time        `json:"revoked_at,omitempty"`
	RevokedBy         *string           `json:"revoked_by,omitempty"`
	RevokeReason      *string           `json:"revoke_reason,omitempty"`
	LastUsedAt        *time.Time        `json:"last_used_at,omitempty"`
	LastUsedIP        *string           `json:"last_used_ip,omitempty"`
	TotalRequests     int64             `json:"total_requests"`
	CreatedAt         time.Time         `json:"created_at"`
	CreatedBy         *string           `json:"created_by,omitempty"`
	UpdatedAt         time.Time         `json:"updated_at"`
	UpdatedBy         *string           `json:"updated_by,omitempty"`
	DeletedAt         *time.Time        `json:"deleted_at,omitempty"`
	DeletedBy         *string           `json:"deleted_by,omitempty"`
}

// IsValid checks if the API key is valid (not revoked and not expired)
func (k *ApiKey) IsValid() bool {
	return !k.IsRevoked() && !k.IsExpired() && k.DeletedAt == nil
}

// IsRevoked checks if the API key has been revoked
func (k *ApiKey) IsRevoked() bool {
	return k.RevokedAt != nil
}

// IsExpired checks if the API key has expired
func (k *ApiKey) IsExpired() bool {
	if k.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*k.ExpiresAt)
}

// IsIPAllowed checks if the given IP address is allowed to use this API key
func (k *ApiKey) IsIPAllowed(ip string) bool {
	// If no whitelist is set, allow all IPs
	if len(k.IPWhitelist) == 0 {
		return true
	}

	clientIP := net.ParseIP(ip)
	if clientIP == nil {
		return false
	}

	for _, allowed := range k.IPWhitelist {
		// Check if it's a CIDR range
		if strings.Contains(allowed, "/") {
			_, cidr, err := net.ParseCIDR(allowed)
			if err == nil && cidr.Contains(clientIP) {
				return true
			}
		} else {
			// Direct IP comparison
			allowedIP := net.ParseIP(allowed)
			if allowedIP != nil && allowedIP.Equal(clientIP) {
				return true
			}
		}
	}

	return false
}

// GetEffectivePermissions returns the effective permissions for this API key
// If scoped permissions are set, returns intersection with user permissions
// If no scoped permissions, returns all user permissions
func (k *ApiKey) GetEffectivePermissions(userPermissions []string) []string {
	// If no scoped permissions, inherit all from user
	if len(k.ScopedPermissions) == 0 {
		return userPermissions
	}

	// Build a set of user permissions for fast lookup
	userPermSet := make(map[string]bool)
	for _, p := range userPermissions {
		userPermSet[p] = true
	}

	// Return intersection of scoped permissions and user permissions
	result := make([]string, 0)
	for _, p := range k.ScopedPermissions {
		if userPermSet[p] {
			result = append(result, p)
		}
	}

	return result
}

// HasPermission checks if the API key has a specific permission
func (k *ApiKey) HasPermission(permission string, userPermissions []string) bool {
	effectivePerms := k.GetEffectivePermissions(userPermissions)
	for _, p := range effectivePerms {
		if p == permission {
			return true
		}
	}
	return false
}
