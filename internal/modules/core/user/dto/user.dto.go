package dto

import "time"

// CreateUserRequest represents request to create a new user
type CreateUserRequest struct {
	Email      string   `json:"email" binding:"required,email"`
	Username   string   `json:"username" binding:"required,min=3,max=100"`
	Password   string   `json:"password" binding:"required,min=8"`
	FullName   *string  `json:"full_name" binding:"omitempty,max=255"`
	Phone      *string  `json:"phone" binding:"omitempty,max=20"`
	RoleID     *string  `json:"role_id" binding:"omitempty,uuid"`
	CompanyIDs []string `json:"company_ids" binding:"omitempty,dive,uuid"`
	// BranchIDs narrows which branches the new user can operate under. Every
	// branch must belong to one of CompanyIDs; for non-super-admin callers the
	// service also enforces the caller's company scope.
	BranchIDs []string `json:"branch_ids" binding:"omitempty,dive,uuid"`
}

// UpdateUserRequest represents request to update a user
type UpdateUserRequest struct {
	Email      *string  `json:"email" binding:"omitempty,email"`
	Username   *string  `json:"username" binding:"omitempty,min=3,max=100"`
	FullName   *string  `json:"full_name" binding:"omitempty,max=255"`
	Phone      *string  `json:"phone" binding:"omitempty,max=20"`
	AvatarURL  *string  `json:"avatar_url" binding:"omitempty,url,max=500"`
	IsActive   *bool    `json:"is_active"`
	RoleID     *string  `json:"role_id" binding:"omitempty,uuid"`
	CompanyIDs []string `json:"company_ids" binding:"omitempty,dive,uuid"`
	// BranchIDs, when non-nil, replaces the user's branch access entirely.
	// Nil means "leave branch assignments untouched"; an empty slice means
	// "revoke all branch access".
	BranchIDs *[]string `json:"branch_ids" binding:"omitempty,dive,uuid"`
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
	ID              string            `json:"id"`
	Email           string            `json:"email"`
	Username        string            `json:"username"`
	FullName        *string           `json:"full_name,omitempty"`
	Phone           *string           `json:"phone,omitempty"`
	AvatarURL       *string           `json:"avatar_url,omitempty"`
	IsActive        bool              `json:"is_active"`
	IsEmailVerified bool              `json:"is_email_verified"`
	RoleName        *string           `json:"role_name,omitempty"`
	Companies       []UserCompanyItem `json:"companies,omitempty"`
	Branches        []UserBranchItem  `json:"branches,omitempty"`
	LastLoginAt     *time.Time        `json:"last_login_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// UserCompanyItem represents a company in the user's membership list
type UserCompanyItem struct {
	CompanyID   string `json:"company_id"`
	CompanyName string `json:"company_name"`
	IsOwner     bool   `json:"is_owner"`
}

// UserBranchItem represents a branch in the user's access list
type UserBranchItem struct {
	BranchID   string `json:"branch_id"`
	BranchCode string `json:"branch_code"`
	BranchName string `json:"branch_name"`
	CompanyID  string `json:"company_id"`
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
	Page     int     `form:"page,default=1" binding:"min=1"`
	Limit    int     `form:"limit,default=10" binding:"min=1,max=1000"`
	Search   string  `form:"search" binding:"omitempty,max=255"`
	IsActive *bool   `form:"is_active"`
	BranchID *string `form:"branch_id" binding:"omitempty,uuid"`

	// Internal tenant-scope fields. These are populated by the handler from
	// the authenticated caller's claims and MUST NOT be bound from the query
	// string (no `form` tag), otherwise a client could bypass tenant isolation.
	ScopeCompanyID *string `form:"-" json:"-"`
	ScopeCreatedBy *string `form:"-" json:"-"`
}
