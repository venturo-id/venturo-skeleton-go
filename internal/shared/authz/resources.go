package authz

import "strings"

// Resources a role can grant. A permission code is "<resource>:<action>",
// e.g. "users:read"; a role stores one level per resource and the level
// expands to actions (see the role module's LevelActions).
//
// Adding a module with permission-checked routes:
//  1. add its resource here, and
//  2. in that module's migration, grant it to the administrator role:
//     UPDATE core.roles SET permissions = permissions || '{"<resource>": "admin"}'
//     WHERE code = 'administrator';
const (
	ResourceUsers     = "users"
	ResourceRoles     = "roles"
	ResourceAuditLogs = "audit_logs"
)

// Resource describes one grantable resource for role editors.
type Resource struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

var registry = []Resource{
	{Code: ResourceUsers, Name: "Users", Description: "User accounts and their role assignments"},
	{Code: ResourceRoles, Name: "Roles", Description: "Roles and the permissions they grant"},
	{Code: ResourceAuditLogs, Name: "Audit logs", Description: "The record of sign-ins and of changes to accounts, roles and API keys (read only)"},
}

// Resources lists every grantable resource in display order.
func Resources() []Resource {
	out := make([]Resource, len(registry))
	copy(out, registry)
	return out
}

// IsResource reports whether code names a registered resource.
func IsResource(code string) bool {
	for _, r := range registry {
		if r.Code == code {
			return true
		}
	}
	return false
}

// SplitPermission splits "resource:action". ok is false when either part
// is missing.
func SplitPermission(perm string) (resource, action string, ok bool) {
	resource, action, ok = strings.Cut(perm, ":")
	return resource, action, ok && resource != "" && action != ""
}
