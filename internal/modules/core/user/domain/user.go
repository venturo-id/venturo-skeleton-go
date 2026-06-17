package domain

import "time"

// User represents a user entity in the core schema
type User struct {
	ID               string     `json:"id"`
	Email            string     `json:"email"`
	Username         string     `json:"username"`
	PasswordHash     string     `json:"-"`
	FullName         *string    `json:"full_name,omitempty"`
	Phone            *string    `json:"phone,omitempty"`
	AvatarURL        *string    `json:"avatar_url,omitempty"`
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

// IsLocked checks if the user account is currently locked
func (u *User) IsLocked() bool {
	if u.LockedUntil == nil {
		return false
	}
	return time.Now().Before(*u.LockedUntil)
}

// CanLogin checks if the user can login
func (u *User) CanLogin() bool {
	return u.IsActive && !u.IsLocked()
}

// UserWithRoles represents user with their assigned roles
type UserWithRoles struct {
	User  User     `json:"user"`
	Roles []string `json:"roles"`
}
