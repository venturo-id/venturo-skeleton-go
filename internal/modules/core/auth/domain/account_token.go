package domain

import "time"

// AccountTokenPurpose is what an account token is for.
type AccountTokenPurpose string

const (
	// Emailed one-time codes.
	PurposeVerifyEmail   AccountTokenPurpose = "verify_email"
	PurposeResetPassword AccountTokenPurpose = "reset_password"
	PurposeChangeEmail   AccountTokenPurpose = "change_email"
	// PurposeResetGrant is the short-lived token a correct password reset
	// code is traded for; it sets the new password.
	PurposeResetGrant AccountTokenPurpose = "reset_grant"
)

// AccountToken is a single-use secret: an emailed code, or a reset grant.
// Only its hash is stored.
type AccountToken struct {
	ID        string
	UserID    string
	Purpose   AccountTokenPurpose
	TokenHash string
	// Email is the account's address when the token was issued; the
	// token is void once the user's email differs. Verification and reset
	// codes are sent to it.
	Email string
	// NewEmail is, for PurposeChangeEmail only, the address the code was
	// sent to and the account moves to.
	NewEmail *string
	// Attempts counts wrong guesses at a code.
	Attempts  int
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}
