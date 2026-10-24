package validator

import (
	"errors"
	"regexp"
	"unicode"
)

// PasswordMinLength and PasswordMaxBytes bound a password. bcrypt only
// uses the first 72 bytes and refuses to hash longer input, so anything
// longer is rejected up front instead of failing deep in the service.
const (
	PasswordMinLength = 8
	PasswordMaxBytes  = 72
)

var (
	ErrPasswordTooShort      = errors.New("password must be at least 8 characters long")
	ErrPasswordTooLong       = errors.New("password must not exceed 72 bytes")
	ErrPasswordNoUppercase   = errors.New("password must contain at least one uppercase letter")
	ErrPasswordNoLowercase   = errors.New("password must contain at least one lowercase letter")
	ErrPasswordNoNumber      = errors.New("password must contain at least one number")
	ErrPasswordTooCommon     = errors.New("password is too common, please choose a stronger password")
	ErrPasswordContainsEmail = errors.New("password must not contain your email address")
)

// ValidatePassword validates password against security requirements
func ValidatePassword(password string) error {
	if len(password) < PasswordMinLength {
		return ErrPasswordTooShort
	}
	if len(password) > PasswordMaxBytes {
		return ErrPasswordTooLong
	}

	var (
		hasUpper  bool
		hasLower  bool
		hasNumber bool
	)

	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsNumber(char):
			hasNumber = true
		}
	}

	if !hasUpper {
		return ErrPasswordNoUppercase
	}

	if !hasLower {
		return ErrPasswordNoLowercase
	}

	if !hasNumber {
		return ErrPasswordNoNumber
	}

	// Check against common passwords
	if isCommonPassword(password) {
		return ErrPasswordTooCommon
	}

	return nil
}

// ValidatePasswordWithEmail validates password and checks if it contains email
func ValidatePasswordWithEmail(password, email string) error {
	// First validate password strength
	if err := ValidatePassword(password); err != nil {
		return err
	}

	// Check if password contains email (case-insensitive)
	emailPattern := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(email))
	if emailPattern.MatchString(password) {
		return ErrPasswordContainsEmail
	}

	// Check if password contains username part of email
	usernamePattern := regexp.MustCompile(`(.+)@`)
	matches := usernamePattern.FindStringSubmatch(email)
	if len(matches) > 1 {
		username := matches[1]
		usernameInPasswordPattern := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(username))
		if usernameInPasswordPattern.MatchString(password) {
			return ErrPasswordContainsEmail
		}
	}

	return nil
}

// isCommonPassword checks if password is in common passwords list
func isCommonPassword(password string) bool {
	// List of most common passwords
	commonPasswords := []string{
		"password", "12345678", "123456789", "qwerty", "abc123",
		"password1", "password123", "12345", "1234567890",
		"letmein", "welcome", "monkey", "dragon", "master",
		"sunshine", "princess", "football", "iloveyou", "admin",
		"welcome123", "admin123", "root", "toor", "pass",
	}

	passwordLower := toLower(password)
	for _, common := range commonPasswords {
		if passwordLower == common {
			return true
		}
	}

	return false
}

// toLower converts string to lowercase
func toLower(s string) string {
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			runes[i] = unicode.ToLower(r)
		}
	}
	return string(runes)
}
