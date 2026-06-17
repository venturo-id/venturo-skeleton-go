package domain

import "time"

// Identity provider codes used in core.user_identities.provider. Keep
// these in sync with whatever the application is wired to verify —
// adding a new provider here without code support is silent dead code.
const (
	IdentityProviderGoogle = "google"
)

// UserIdentity is a link between a core.users row and a user record on
// an external identity provider (Google via Firebase, Apple, …).
type UserIdentity struct {
	ID             string  `json:"id"`
	UserID         string  `json:"user_id"`
	Provider       string  `json:"provider"`
	ProviderUserID string  `json:"provider_user_id"`
	Email          *string `json:"email,omitempty"`
	// RawProfile is the JSONB snapshot of provider claims. Carried as
	// raw bytes so the repository layer can hand it to pgx without
	// re-parsing.
	RawProfile  []byte     `json:"-"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
