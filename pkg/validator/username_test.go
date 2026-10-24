package validator

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		username string
		want     error
	}{
		{"john", nil},
		{"John.Doe-2_x", nil},
		{"abc", nil},
		{strings.Repeat("a", 50), nil},
		{"ab", ErrUsernameTooShort},
		{strings.Repeat("a", 51), ErrUsernameTooLong},
		{"victim@example.com", ErrInvalidUsernameFormat},
		{"john doe", ErrInvalidUsernameFormat},
		{"_john", ErrUsernameStartsInvalid},
		{".john", ErrUsernameStartsInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.username, func(t *testing.T) {
			if err := ValidateUsername(tt.username); !errors.Is(err, tt.want) {
				t.Errorf("ValidateUsername(%q) = %v, want %v", tt.username, err, tt.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	if got := NormalizeUsername("  John.Doe "); got != "john.doe" {
		t.Errorf("NormalizeUsername() = %q, want %q", got, "john.doe")
	}
	if got := NormalizeEmail(" User@Example.COM "); got != "user@example.com" {
		t.Errorf("NormalizeEmail() = %q, want %q", got, "user@example.com")
	}
}
