package jwt

import (
	"testing"
	"time"
)

func TestGetRefreshExpirationTime(t *testing.T) {
	tests := []struct {
		name string
		env  string
		set  bool
		want time.Duration
	}{
		{name: "unset defaults to 168h", set: false, want: 168 * time.Hour},
		{name: "valid duration parsed", env: "24h", set: true, want: 24 * time.Hour},
		{name: "garbage falls back to 168h", env: "not-a-duration", set: true, want: 168 * time.Hour},
		{name: "empty string defaults to 168h", env: "", set: true, want: 168 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("JWT_REFRESH_EXPIRATION", tt.env)
			} else {
				// Ensure no ambient value leaks in from the environment.
				t.Setenv("JWT_REFRESH_EXPIRATION", "")
			}
			if got := GetRefreshExpirationTime(); got != tt.want {
				t.Fatalf("GetRefreshExpirationTime() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGenerateTokenHasUniqueJTI verifies every access token carries a
// non-empty jti (RegisteredClaims.ID) and that two tokens minted
// back-to-back get distinct ids — the property the denylist relies on to
// revoke one token without touching others.
func TestGenerateTokenHasUniqueJTI(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-at-least-32-characters-long!!")

	tok1, err := GenerateToken("user-1", "", "", "", "", "u1@example.com", "u1", "User One", false, nil)
	if err != nil {
		t.Fatalf("GenerateToken #1: %v", err)
	}
	tok2, err := GenerateToken("user-1", "", "", "", "", "u1@example.com", "u1", "User One", false, nil)
	if err != nil {
		t.Fatalf("GenerateToken #2: %v", err)
	}

	c1, err := ParseToken(tok1)
	if err != nil {
		t.Fatalf("ParseToken #1: %v", err)
	}
	c2, err := ParseToken(tok2)
	if err != nil {
		t.Fatalf("ParseToken #2: %v", err)
	}

	if c1.ID == "" {
		t.Fatal("expected non-empty jti on access token, got empty")
	}
	if c1.ID == c2.ID {
		t.Fatalf("expected distinct jti per token, both were %q", c1.ID)
	}
}
