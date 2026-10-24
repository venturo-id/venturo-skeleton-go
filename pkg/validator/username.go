package validator

import (
	"errors"
	"regexp"
	"strings"
)

const (
	UsernameMinLength = 3
	UsernameMaxLength = 50
)

var (
	ErrUsernameTooShort      = errors.New("username must be at least 3 characters long")
	ErrUsernameTooLong       = errors.New("username must not exceed 50 characters")
	ErrInvalidUsernameFormat = errors.New("username can only contain letters, numbers, dots, underscores, and hyphens")
	ErrUsernameStartsInvalid = errors.New("username must start with a letter or number")
)

// usernameRegex: letters, digits, dot, underscore, hyphen; starts with a
// letter or digit. No "@", so a username can never be mistaken for an
// email address at sign-in.
var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidateUsername validates username format
func ValidateUsername(username string) error {
	if len(username) < UsernameMinLength {
		return ErrUsernameTooShort
	}

	if len(username) > UsernameMaxLength {
		return ErrUsernameTooLong
	}

	if !usernameRegex.MatchString(username) {
		if !isAlphanumeric(username[0]) {
			return ErrUsernameStartsInvalid
		}
		return ErrInvalidUsernameFormat
	}

	return nil
}

// NormalizeUsername lowercases and trims a username. Usernames are stored
// normalized, so "Admin" and "admin" are the same account.
func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func isAlphanumeric(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
