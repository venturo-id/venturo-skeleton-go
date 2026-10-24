package domain

import "time"

// Permissions represents level-based permissions stored as JSONB.
// Format: {"resource": "level"} where resource is registered in
// authz.Resources and level is "viewer", "editor", or "admin".
// Example: {"users": "admin", "roles": "viewer"}
type Permissions map[string]string

// ToStringList expands level-based permissions to flat action list
// Example: {"roles": "viewer"} → ["roles:read"]
// Example: {"users": "editor"} → ["users:create", "users:read", "users:update", "users:delete"]
func (p Permissions) ToStringList() []string {
	var result []string
	for resource, level := range p {
		for _, action := range GetActionsForLevel(level) {
			result = append(result, resource+":"+action)
		}
	}
	return result
}

// Merge merges another Permissions into this one
// Higher level wins when same resource exists in both
func (p Permissions) Merge(other Permissions) {
	for resource, level := range other {
		existing, exists := p[resource]
		if !exists || levelRank[PermissionLevel(level)] > levelRank[PermissionLevel(existing)] {
			p[resource] = level
		}
	}
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
