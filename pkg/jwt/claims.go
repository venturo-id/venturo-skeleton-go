package jwt

import (
	"github.com/golang-jwt/jwt/v5"
)

// RoleSuperAdmin is the constant for the super admin role code
const RoleSuperAdmin = "super_admin"

// Claims represents custom JWT claims for Tuai.
//
// Permissions are intentionally NOT embedded here — they live in Redis
// (see internal/shared/authz) and are looked up on the request path.
// That keeps the JWT small enough to survive every proxy / ingress in
// the chain and lets admin-side permission changes take effect without
// waiting for the token to expire.
//
// Roles are kept inline because the set is small (1–2 entries per user)
// and middleware.RequireRole checks them synchronously with no cache.
type Claims struct {
	UserID       string   `json:"user_id"`
	Email        string   `json:"email"`
	Username     string   `json:"username"`
	FullName     string   `json:"full_name"`
	IsSuperAdmin bool     `json:"is_super_admin"` // Bypasses all permission checks
	Roles        []string `json:"roles"`

	// ScopedPermissions is a runtime-only permission set used when a
	// request is authenticated via API key. API keys can carry a
	// narrower scope than the owning user's full permissions, so they
	// bypass the Redis cache (which holds the user's *full* set) and
	// pin an explicit list here. Never serialised into the JWT — the
	// `json:"-"` tag is intentional.
	ScopedPermissions []string `json:"-"`

	jwt.RegisteredClaims
}

// HasScopedPermission is a helper for the API-key flow: true when the
// request-scoped set includes the given permission. Not used by the
// regular JWT flow (which reads from authz/Redis instead).
func (c *Claims) HasScopedPermission(permission string) bool {
	for _, p := range c.ScopedPermissions {
		if p == permission {
			return true
		}
	}
	return false
}

// HasRole checks if user has a specific role
func (c *Claims) HasRole(role string) bool {
	for _, r := range c.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// HasAnyRole checks if user has at least one of the specified roles
func (c *Claims) HasAnyRole(roles ...string) bool {
	for _, role := range roles {
		if c.HasRole(role) {
			return true
		}
	}
	return false
}

// HasAllRoles checks if user has all specified roles
func (c *Claims) HasAllRoles(roles ...string) bool {
	for _, role := range roles {
		if !c.HasRole(role) {
			return false
		}
	}
	return true
}
