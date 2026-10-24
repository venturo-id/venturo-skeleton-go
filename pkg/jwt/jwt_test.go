package jwt

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-that-is-long-enough-for-hs256-signing"

func TestValidateSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		wantErr bool
	}{
		{
			name:    "unset",
			secret:  "",
			wantErr: true,
		},
		{
			name:    "shorter than minimum",
			secret:  "too-short",
			wantErr: true,
		},
		{
			name:    "old built-in default",
			secret:  "tuai-development-secret-change-in-production",
			wantErr: true,
		},
		{
			name:    ".env.example placeholder",
			secret:  "your-super-secret-key-change-in-production-min-32-characters",
			wantErr: true,
		},
		{
			name:    "exactly minimum length",
			secret:  strings.Repeat("a", MinSecretLength),
			wantErr: false,
		},
		{
			name:    "strong secret",
			secret:  testSecret,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("JWT_SECRET", tt.secret)

			err := ValidateSecret()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSecret() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidJWTSecret) {
				t.Errorf("ValidateSecret() error = %v, want it to wrap ErrInvalidJWTSecret", err)
			}
		})
	}
}

func TestGenerateToken_RefusesWithoutSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "")

	if _, err := GenerateToken("user-1", "a@example.com", "alice", "Alice", true, nil, ""); err == nil {
		t.Fatal("GenerateToken() succeeded without JWT_SECRET; want an error")
	}
}

func TestParseToken_RejectsTokenSignedWithOtherSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", testSecret)
	tokenString, err := GenerateToken("user-1", "a@example.com", "alice", "Alice", true, nil, "")
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	t.Setenv("JWT_SECRET", strings.Repeat("b", MinSecretLength))
	if _, err := ParseToken(tokenString); err == nil {
		t.Fatal("ParseToken() accepted a token signed with a different secret")
	}
}

func TestParseToken_RoundTrip(t *testing.T) {
	t.Setenv("JWT_SECRET", testSecret)

	tokenString, err := GenerateToken("user-1", "a@example.com", "alice", "Alice", false, []string{"administrator"}, "")
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	claims, err := ParseToken(tokenString)
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	if claims.UserID != "user-1" || claims.IsSuperAdmin || len(claims.Roles) != 1 || claims.Roles[0] != "administrator" {
		t.Errorf("ParseToken() claims = %+v, want user-1 / not super admin / administrator", claims)
	}
	if claims.Issuer != Issuer {
		t.Errorf("issuer = %q, want %q", claims.Issuer, Issuer)
	}
}

// sign signs claims with the test secret using method.
func sign(t *testing.T, method jwt.SigningMethod, claims jwt.Claims) string {
	t.Helper()
	tokenString, err := jwt.NewWithClaims(method, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return tokenString
}

func TestParseToken_RejectsForeignTokens(t *testing.T) {
	t.Setenv("JWT_SECRET", testSecret)
	now := time.Now()
	valid := jwt.RegisteredClaims{
		Issuer:    Issuer,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
	}

	otherIssuer := valid
	otherIssuer.Issuer = "tuai-api"
	noExpiry := valid
	noExpiry.ExpiresAt = nil

	tests := []struct {
		name  string
		token string
	}{
		{"issued by another application", sign(t, jwt.SigningMethodHS256, &Claims{UserID: "u", RegisteredClaims: otherIssuer})},
		{"signed with another algorithm", sign(t, jwt.SigningMethodHS512, &Claims{UserID: "u", RegisteredClaims: valid})},
		{"without an expiry", sign(t, jwt.SigningMethodHS256, &Claims{UserID: "u", RegisteredClaims: noExpiry})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseToken(tt.token); err == nil {
				t.Fatal("ParseToken() accepted the token")
			}
		})
	}

	if _, err := ParseToken(sign(t, jwt.SigningMethodHS256, &Claims{UserID: "u", RegisteredClaims: valid})); err != nil {
		t.Fatalf("ParseToken() rejected a valid token: %v", err)
	}
}

func TestIssuedAt_KeepsMilliseconds(t *testing.T) {
	t.Setenv("JWT_SECRET", testSecret)

	issued := time.Unix(1761187200, 123_000_000)
	tokenString := sign(t, jwt.SigningMethodHS256, &Claims{UserID: "u", RegisteredClaims: jwt.RegisteredClaims{
		Issuer:    Issuer,
		IssuedAt:  jwt.NewNumericDate(issued),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}})

	claims, err := ParseToken(tokenString)
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	// The JSON number is a float: reading it back may land 1 ms early,
	// never late.
	got, want := claims.IssuedAt.UnixMilli(), issued.UnixMilli()
	if got != want && got != want-1 {
		t.Errorf("iat = %d ms, want %d ms (or 1 ms less): session revocation needs sub-second issue times", got, want)
	}
}

func TestParseToken_ToleratesClockSkew(t *testing.T) {
	t.Setenv("JWT_SECRET", testSecret)
	claimsFrom := func(issued time.Time) *Claims {
		return &Claims{UserID: "u", RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			IssuedAt:  jwt.NewNumericDate(issued),
			NotBefore: jwt.NewNumericDate(issued),
			ExpiresAt: jwt.NewNumericDate(issued.Add(time.Minute)),
		}}
	}

	// Signed by a replica whose clock runs a little ahead.
	if _, err := ParseToken(sign(t, jwt.SigningMethodHS256, claimsFrom(time.Now().Add(50*time.Millisecond)))); err != nil {
		t.Fatalf("ParseToken() rejected a token from a clock 50 ms ahead: %v", err)
	}
	// Not one from the future.
	if _, err := ParseToken(sign(t, jwt.SigningMethodHS256, claimsFrom(time.Now().Add(time.Minute)))); err == nil {
		t.Fatal("ParseToken() accepted a token issued a minute from now")
	}
}

func TestGetExpirationTime(t *testing.T) {
	t.Setenv("JWT_EXPIRATION", "")
	if got := GetExpirationTime(); got != DefaultExpiration {
		t.Errorf("unset: %s, want %s", got, DefaultExpiration)
	}
	t.Setenv("JWT_EXPIRATION", " 5m ")
	if got := GetExpirationTime(); got != 5*time.Minute {
		t.Errorf("5m: %s, want 5m", got)
	}
	t.Setenv("JWT_EXPIRATION", "-1h")
	if got := GetExpirationTime(); got != DefaultExpiration {
		t.Errorf("negative: %s, want %s", got, DefaultExpiration)
	}
}
