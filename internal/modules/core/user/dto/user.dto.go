package dto

import "time"

// CreateUserRequest represents request to create a new user
type CreateUserRequest struct {
	Email    string  `json:"email" binding:"required,email,max=254"`
	Username string  `json:"username" binding:"required,username"`
	Password string  `json:"password" binding:"required,password"`
	FullName *string `json:"full_name" binding:"omitempty,max=255"`
	Phone    *string `json:"phone" binding:"omitempty,max=20"`
	// Timezone is an IANA zone name; missing or null means Asia/Jakarta.
	Timezone *string `json:"timezone" binding:"omitempty,timezone"`
	RoleID   *string `json:"role_id" binding:"omitempty,uuid"`
}

// UpdateUserRequest represents request to update a user. There is no
// avatar_url: an avatar only ever points at the bucket, and is set by its
// owner through PUT /users/me/avatar. A client still sending the field is
// ignored, as is any unknown field.
type UpdateUserRequest struct {
	Email    *string `json:"email" binding:"omitempty,email,max=254"`
	Username *string `json:"username" binding:"omitempty,username"`
	FullName *string `json:"full_name" binding:"omitempty,max=255"`
	Phone    *string `json:"phone" binding:"omitempty,max=20"`
	Timezone *string `json:"timezone" binding:"omitempty,timezone"`
	IsActive *bool   `json:"is_active"`
	// IsEmailVerified lets an admin vouch for (or withdraw) someone else's
	// address. A role can only be assigned to a verified address.
	IsEmailVerified *bool `json:"is_email_verified"`
	// RoleID, when non-nil, replaces the user's roles with this one. Nil
	// means "leave role assignments untouched".
	RoleID *string `json:"role_id" binding:"omitempty,uuid"`
}

// UpdateMeRequest represents request to update current user profile. The
// avatar changes through PUT /users/me/avatar; an avatar_url sent here is
// ignored.
type UpdateMeRequest struct {
	FullName *string `json:"full_name" binding:"omitempty,max=255"`
	Phone    *string `json:"phone" binding:"omitempty,max=20"`
	// Timezone is an IANA zone name, e.g. the browser's
	// Intl.DateTimeFormat().resolvedOptions().timeZone.
	Timezone *string `json:"timezone" binding:"omitempty,timezone"`
}

// ChangePasswordRequest represents request to change password
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required,max=72"`
	NewPassword     string `json:"new_password" binding:"required,password"`
}

// UserResponse represents user data in response
type UserResponse struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	Username        string     `json:"username"`
	FullName        *string    `json:"full_name,omitempty"`
	Phone           *string    `json:"phone,omitempty"`
	AvatarURL       *string    `json:"avatar_url,omitempty"`
	Timezone        string     `json:"timezone"`
	IsActive        bool       `json:"is_active"`
	IsEmailVerified bool       `json:"is_email_verified"`
	RoleName        *string    `json:"role_name,omitempty"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// UserQueryParams represents query parameters for listing users
type UserQueryParams struct {
	Page     int    `form:"page,default=1" binding:"min=1,max=100000"`
	Limit    int    `form:"limit,default=10" binding:"min=1,max=100"`
	Search   string `form:"search" binding:"omitempty,max=255"`
	IsActive *bool  `form:"is_active"`
}
