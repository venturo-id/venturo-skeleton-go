package domain

// PermissionLevel represents a simplified permission level
type PermissionLevel string

const (
	LevelViewer PermissionLevel = "viewer"
	LevelEditor PermissionLevel = "editor"
	LevelAdmin  PermissionLevel = "admin"
)

// LevelActions maps each permission level to its allowed actions.
// This config is the single source of truth for what each level can do.
// Levels are hierarchical: admin > editor > viewer.
var LevelActions = map[PermissionLevel][]string{
	LevelViewer: {"read"},
	LevelEditor: {"create", "read", "update", "delete"},
	LevelAdmin:  {"create", "read", "update", "delete", "export", "import", "restore"},
}

// ValidLevels is the list of all valid permission levels
var ValidLevels = []PermissionLevel{LevelViewer, LevelEditor, LevelAdmin}

// IsValidLevel checks if a string is a valid permission level
func IsValidLevel(level string) bool {
	for _, l := range ValidLevels {
		if string(l) == level {
			return true
		}
	}
	return false
}

// GetActionsForLevel returns the actions associated with a permission level
func GetActionsForLevel(level string) []string {
	actions, ok := LevelActions[PermissionLevel(level)]
	if !ok {
		return nil
	}
	return actions
}

// LevelHasAction checks if a permission level includes a specific action
func LevelHasAction(level, action string) bool {
	actions := GetActionsForLevel(level)
	for _, a := range actions {
		if a == action {
			return true
		}
	}
	return false
}
