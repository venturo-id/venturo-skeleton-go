package validator

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		want     error
	}{
		{"strong", "Timebox2026", nil},
		{"exactly 72 bytes", "Aa1" + strings.Repeat("x", 69), nil},
		{"too short", "Ab1", ErrPasswordTooShort},
		{"over bcrypt's 72 bytes", "Aa1" + strings.Repeat("x", 70), ErrPasswordTooLong},
		{"no uppercase", "timebox2026", ErrPasswordNoUppercase},
		{"no lowercase", "TIMEBOX2026", ErrPasswordNoLowercase},
		{"no number", "TimeboxTimebox", ErrPasswordNoNumber},
		{"common", "Password1", ErrPasswordTooCommon},
		{"all lowercase common", "password123", ErrPasswordNoUppercase},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidatePassword(tt.password); !errors.Is(err, tt.want) {
				t.Errorf("ValidatePassword(%q) = %v, want %v", tt.password, err, tt.want)
			}
		})
	}
}

func TestValidatePasswordWithEmail(t *testing.T) {
	if err := ValidatePasswordWithEmail("Johnny2026x", "johnny@example.com"); !errors.Is(err, ErrPasswordContainsEmail) {
		t.Errorf("password containing the email name: err = %v, want %v", err, ErrPasswordContainsEmail)
	}
	if err := ValidatePasswordWithEmail("Timebox2026", "johnny@example.com"); err != nil {
		t.Errorf("unrelated password: err = %v, want nil", err)
	}
}
