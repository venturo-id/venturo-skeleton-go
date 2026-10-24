package jwt

import (
	"github.com/golang-jwt/jwt/v5"
)

// RoleSuperAdmin is the constant for the super admin role code
const RoleSuperAdmin = "super_admin"

// Claims represents custom JWT claims for Timebox.
//
// The token only identifies the user. Permissions are intentionally NOT
// embedded here — they live in Redis (see internal/shared/authz) and are
// looked up on the request path, which keeps the JWT small and lets
// admin-side changes take effect without waiting for the token to expire.
//
// IsSuperAdmin and Roles describe the user when the token was issued, for
// the frontend's convenience. They go stale and must never be used for
// authorization: use middleware.GetSubject / middleware.IsSuperAdmin.
type Claims struct {
	UserID       string   `json:"user_id"`
	Email        string   `json:"email"`
	Username     string   `json:"username"`
	FullName     string   `json:"full_name"`
	IsSuperAdmin bool     `json:"is_super_admin"`
	Roles        []string `json:"roles"`
	// SessionID names the sign-in session the token belongs to, so ending
	// that session ends the token too (see authz.Subject.AcceptsSession).
	// Empty in tokens issued before sessions had ids.
	SessionID string `json:"sid,omitempty"`

	// ScopedPermissions is a runtime-only permission set used when a
	// request is authenticated via API key: the key's own scope, which
	// the middleware narrows to what the owner still holds. Never
	// serialised into the JWT — the `json:"-"` tag is intentional.
	ScopedPermissions []string `json:"-"`

	jwt.RegisteredClaims
}
