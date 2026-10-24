package dto

import (
	"time"

	apikeyDTO "timebox-be/internal/modules/core/apikey/dto"
	"timebox-be/internal/modules/core/auth/domain"
	"timebox-be/internal/shared/audit"
)

// SessionResponse is one of the caller's sign-in sessions.
type SessionResponse struct {
	ID           string            `json:"id"`
	Device       domain.DeviceInfo `json:"device"`
	IPAddress    *string           `json:"ip_address,omitempty"`
	SignedInAt   time.Time         `json:"signed_in_at"`
	LastActiveAt time.Time         `json:"last_active_at"`
	ExpiresAt    time.Time         `json:"expires_at"`
	// Current marks the session the request was made from.
	Current bool `json:"current"`
}

// ReauthRequest proves again that the caller owns the account, before a
// change that could hand it to someone else: exactly one of the current
// password, or a Google sign-in made just now into the Google account
// linked to this one — the only way for an account without a password.
type ReauthRequest struct {
	CurrentPassword string      `json:"current_password" binding:"omitempty,max=72"`
	Google          *GoogleCode `json:"google"`
}

// ChangeEmailRequest is the body of POST /account/email.
type ChangeEmailRequest struct {
	NewEmail string `json:"new_email" binding:"required,email,max=254"`
	ReauthRequest
}

// EmailChangeSent answers POST /account/email and /account/email/resend:
// a code went to NewEmail.
type EmailChangeSent struct {
	NewEmail string `json:"new_email"`
	CodeTicket
}

// UserResponse wraps the caller's account after a change to it.
type UserResponse struct {
	User UserInfo `json:"user"`
}

// AccountExport is everything the account holds about its owner.
type AccountExport struct {
	ExportedAt     time.Time                  `json:"exported_at"`
	Profile        ExportProfile              `json:"profile"`
	Roles          []string                   `json:"roles"`
	LinkedAccounts []ExportLinkedAccount      `json:"linked_accounts"`
	Sessions       []SessionResponse          `json:"sessions"`
	APIKeys        []apikeyDTO.APIKeyResponse `json:"api_keys"`
	// Activity is the owner's most recent entries in the audit log.
	Activity []audit.Entry `json:"activity"`
}

// ExportProfile is the account row.
type ExportProfile struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	Username        string     `json:"username"`
	FullName        *string    `json:"full_name,omitempty"`
	Phone           *string    `json:"phone,omitempty"`
	AvatarURL       *string    `json:"avatar_url,omitempty"`
	Timezone        string     `json:"timezone"`
	HasPassword     bool       `json:"has_password"`
	IsEmailVerified bool       `json:"is_email_verified"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// ExportLinkedAccount is an external sign-in (e.g. Google) linked to the
// account.
type ExportLinkedAccount struct {
	Provider    string     `json:"provider"`
	Email       *string    `json:"email,omitempty"`
	LinkedAt    time.Time  `json:"linked_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}
