package dto

import "time"

// CreateUserRequest represents request to create a new user
type CreateUserRequest struct {
	Email    string  `json:"email" binding:"required,email"`
	Username string  `json:"username" binding:"required,min=3,max=100"`
	Password string  `json:"password" binding:"required,min=8"`
	FullName *string `json:"full_name" binding:"omitempty,max=255"`
	Phone    *string `json:"phone" binding:"omitempty,max=20"`
	RoleID   *string `json:"role_id" binding:"omitempty,uuid"`
}

// UpdateUserRequest represents request to update a user
type UpdateUserRequest struct {
	Email     *string `json:"email" binding:"omitempty,email"`
	Username  *string `json:"username" binding:"omitempty,min=3,max=100"`
	FullName  *string `json:"full_name" binding:"omitempty,max=255"`
	Phone     *string `json:"phone" binding:"omitempty,max=20"`
	AvatarURL *string `json:"avatar_url" binding:"omitempty,url,max=500"`
	IsActive  *bool   `json:"is_active"`
	// RoleID, when non-nil, replaces the user's roles with this one. Nil
	// means "leave role assignments untouched".
	RoleID *string `json:"role_id" binding:"omitempty,uuid"`
}

// UpdateMeRequest represents request to update current user profile
type UpdateMeRequest struct {
	FullName  *string `json:"full_name" binding:"omitempty,max=255"`
	Phone     *string `json:"phone" binding:"omitempty,max=20"`
	AvatarURL *string `json:"avatar_url" binding:"omitempty,url,max=500"`
}

// ChangePasswordRequest represents request to change password
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=8"`
}

// UserResponse represents user data in response
type UserResponse struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	Username        string     `json:"username"`
	FullName        *string    `json:"full_name,omitempty"`
	Phone           *string    `json:"phone,omitempty"`
	AvatarURL       *string    `json:"avatar_url,omitempty"`
	IsActive        bool       `json:"is_active"`
	IsEmailVerified bool       `json:"is_email_verified"`
	RoleName        *string    `json:"role_name,omitempty"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// UserListResponse represents paginated user list (used internally)
type UserListResponse struct {
	Users []UserResponse `json:"users"`
	Total int64          `json:"total"`
	Page  int            `json:"page"`
	Limit int            `json:"limit"`
}

// UserQueryParams represents query parameters for listing users
type UserQueryParams struct {
	Page     int    `form:"page,default=1" binding:"min=1"`
	Limit    int    `form:"limit,default=10" binding:"min=1,max=1000"`
	Search   string `form:"search" binding:"omitempty,max=255"`
	IsActive *bool  `form:"is_active"`
}
