package domain

import "time"

// DefaultTimezone is the zone of an account that has not chosen one; the
// users.timezone column defaults to it too (migration 000010).
const DefaultTimezone = "Asia/Jakarta"

// User represents a user entity in the core schema
type User struct {
	ID           string  `json:"id"`
	Email        string  `json:"email"`
	Username     string  `json:"username"`
	PasswordHash string  `json:"-"`
	FullName     *string `json:"full_name,omitempty"`
	Phone        *string `json:"phone,omitempty"`
	AvatarURL    *string `json:"avatar_url,omitempty"`
	// Timezone is the IANA zone the user's dates are shown and grouped in.
	// Timestamps are stored in UTC either way.
	Timezone         string     `json:"timezone"`
	IsActive         bool       `json:"is_active"`
	IsEmailVerified  bool       `json:"is_email_verified"`
	EmailVerifiedAt  *time.Time `json:"email_verified_at,omitempty"`
	FailedLoginCount int        `json:"failed_login_count"`
	LockedUntil      *time.Time `json:"locked_until,omitempty"`
	LastLoginAt      *time.Time `json:"last_login_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	CreatedBy        *string    `json:"created_by,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
	UpdatedBy        *string    `json:"updated_by,omitempty"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
	DeletedBy        *string    `json:"deleted_by,omitempty"`
}

// MarkEmailVerified sets or clears the verified flag and its timestamp
// together.
func (u *User) MarkEmailVerified(verified bool, at time.Time) {
	u.IsEmailVerified = verified
	if verified {
		u.EmailVerifiedAt = &at
	} else {
		u.EmailVerifiedAt = nil
	}
}
