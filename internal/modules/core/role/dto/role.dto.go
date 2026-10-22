package dto

import "time"

// CreateRoleRequest represents request to create a new role
type CreateRoleRequest struct {
	Code        string            `json:"code" binding:"required,min=2,max=50"`
	Name        string            `json:"name" binding:"required,min=2,max=100"`
	Description *string           `json:"description" binding:"omitempty,max=1000"`
	Permissions map[string]string `json:"permissions" binding:"required"`
	IsActive    *bool             `json:"is_active"`
}

// UpdateRoleRequest represents request to update a role
type UpdateRoleRequest struct {
	Name        *string           `json:"name" binding:"omitempty,min=2,max=100"`
	Description *string           `json:"description" binding:"omitempty,max=1000"`
	Permissions map[string]string `json:"permissions" binding:"omitempty"`
	IsActive    *bool             `json:"is_active"`
}

// UpdatePermissionsRequest represents request to update role permissions
type UpdatePermissionsRequest struct {
	Permissions map[string]string `json:"permissions" binding:"required"`
}

// RoleResponse represents role data in response
type RoleResponse struct {
	ID          string            `json:"id"`
	Code        string            `json:"code"`
	Name        string            `json:"name"`
	Description *string           `json:"description,omitempty"`
	Permissions map[string]string `json:"permissions"`
	IsSystem    bool              `json:"is_system"`
	IsActive    bool              `json:"is_active"`
	CreatedAt   time.Time         `json:"created_at"`
	CreatedBy   *string           `json:"created_by,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// RoleListResponse represents paginated role list (used internally)
type RoleListResponse struct {
	Roles []RoleResponse `json:"roles"`
	Total int64          `json:"total"`
	Page  int            `json:"page"`
	Limit int            `json:"limit"`
}

// RoleQueryParams represents query parameters for listing roles
type RoleQueryParams struct {
	Page          int      `form:"page,default=1" binding:"min=1"`
	Limit         int      `form:"limit,default=10" binding:"min=1,max=100"`
	Search        string   `form:"search" binding:"omitempty,max=255"`
	ExcludeSystem *bool    `form:"-"` // Internal use only: exclude is_system roles
	ExcludeCodes  []string `form:"-"` // Internal use only: exclude roles with these codes
}

// AssignRoleRequest represents request to assign a role to a user
type AssignRoleRequest struct {
	UserID string `json:"user_id" binding:"required,uuid"`
	RoleID string `json:"role_id" binding:"required,uuid"`
}

// RemoveRoleRequest represents request to remove a role from a user
type RemoveRoleRequest struct {
	UserID string `json:"user_id" binding:"required,uuid"`
	RoleID string `json:"role_id" binding:"required,uuid"`
}
