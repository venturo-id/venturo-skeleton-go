package dto

// MeResponse is the shape returned by GET /core/v1/auth/me.
//
// It is intentionally shaped like SignInResponse minus the tokens, so
// FE code that already knows how to read signin data can reuse the
// same store/rendering path when it rehydrates from /me after a page
// reload or when it re-syncs after a 403.
//
// `permissions` reflects the caller's *current* effective set (from
// Redis / authz cache, falling back to DB on cache miss) — not a
// snapshot of what the JWT was minted with.
type MeResponse struct {
	User        UserInfo `json:"user"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`

	// IsSuperAdmin mirrors the JWT claim of the same name. Exposed here
	// so FE can render admin-only UI without decoding the JWT. Super
	// admins effectively bypass `permissions` on the backend.
	IsSuperAdmin bool `json:"is_super_admin"`
}
