package domain

import "time"

// Permissions represents level-based permissions stored as JSONB
// Format: {"resource": "level"} where level is "viewer", "editor", or "admin"
// Example: {"dashboard": "viewer", "user-management": "admin", "jurnal-umum": "editor"}
type Permissions map[string]string

// HasPermission checks if the permissions grant a specific resource:action
// It expands the stored level to actions using the LevelActions config
func (p Permissions) HasPermission(resource, action string) bool {
	level, exists := p[resource]
	if !exists {
		return false
	}
	return LevelHasAction(level, action)
}

// HasAnyPermission checks if any of the specified permissions exist
// Format: "resource:action"
func (p Permissions) HasAnyPermission(perms ...string) bool {
	for _, perm := range perms {
		resource, action := splitPermission(perm)
		if p.HasPermission(resource, action) {
			return true
		}
	}
	return false
}

// HasAllPermissions checks if all specified permissions exist
// Format: "resource:action"
func (p Permissions) HasAllPermissions(perms ...string) bool {
	for _, perm := range perms {
		resource, action := splitPermission(perm)
		if !p.HasPermission(resource, action) {
			return false
		}
	}
	return true
}

// ToStringList expands level-based permissions to flat action list
// Example: {"dashboard": "viewer"} → ["dashboard:read"]
// Example: {"users": "editor"} → ["users:create", "users:read", "users:update", "users:delete"]
func (p Permissions) ToStringList() []string {
	var result []string
	for resource, level := range p {
		actions := GetActionsForLevel(level)
		for _, action := range actions {
			result = append(result, resource+":"+action)
		}
	}
	return result
}

// Merge merges another Permissions into this one
// Higher level wins when same resource exists in both
func (p Permissions) Merge(other Permissions) {
	levelRank := map[string]int{
		"viewer": 1,
		"editor": 2,
		"admin":  3,
	}

	for resource, level := range other {
		existing, exists := p[resource]
		if !exists || levelRank[level] > levelRank[existing] {
			p[resource] = level
		}
	}
}

// splitPermission splits "resource:action" into resource and action
func splitPermission(perm string) (string, string) {
	for i, c := range perm {
		if c == ':' {
			return perm[:i], perm[i+1:]
		}
	}
	return perm, ""
}

// Role represents a role entity with level-based permissions
type Role struct {
	ID          string      `json:"id"`
	Code        string      `json:"code"`
	Name        string      `json:"name"`
	Description *string     `json:"description,omitempty"`
	Permissions Permissions `json:"permissions"`
	IsSystem    bool        `json:"is_system"`
	IsActive    bool        `json:"is_active"`
	CreatedAt   time.Time   `json:"created_at"`
	CreatedBy   *string     `json:"created_by,omitempty"`
	UpdatedAt   time.Time   `json:"updated_at"`
	UpdatedBy   *string     `json:"updated_by,omitempty"`
	DeletedAt   *time.Time  `json:"deleted_at,omitempty"`
	DeletedBy   *string     `json:"deleted_by,omitempty"`
}

// UserRole represents a user-role assignment
type UserRole struct {
	UserID    string    `json:"user_id"`
	RoleID    string    `json:"role_id"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy *string   `json:"created_by,omitempty"`
}
