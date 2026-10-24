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

// ValidLevels is the list of all valid permission levels, lowest first.
var ValidLevels = []PermissionLevel{LevelViewer, LevelEditor, LevelAdmin}

// levelRank orders levels for Merge; unknown levels rank 0.
var levelRank = map[PermissionLevel]int{
	LevelViewer: 1,
	LevelEditor: 2,
	LevelAdmin:  3,
}

// IsValidLevel checks if a string is a valid permission level
func IsValidLevel(level string) bool {
	_, ok := levelRank[PermissionLevel(level)]
	return ok
}

// IsValidAction reports whether any level grants action.
func IsValidAction(action string) bool {
	for _, a := range LevelActions[LevelAdmin] {
		if a == action {
			return true
		}
	}
	return false
}

// GetActionsForLevel returns the actions associated with a permission level
func GetActionsForLevel(level string) []string {
	return LevelActions[PermissionLevel(level)]
}
