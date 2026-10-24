package jwt

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken     = errors.New("invalid token")
	ErrExpiredToken     = errors.New("token has expired")
	ErrTokenNotProvided = errors.New("token not provided")
	ErrInvalidSignature = errors.New("invalid token signature")
	ErrInvalidJWTSecret = errors.New("JWT_SECRET not set or too short")
)

const (
	MinSecretLength = 32 // Minimum length for JWT secret in characters

	// Issuer is set on every token this API signs, and required on every
	// token it accepts.
	Issuer = "timebox-api"

	// DefaultExpiration applies when JWT_EXPIRATION is unset or invalid.
	// Access tokens are kept short: ending sessions refuses them at once
	// (see authz.Subject), but only by a lookup on every request.
	DefaultExpiration = 15 * time.Minute

	// clockSkewLeeway absorbs clock differences between the replicas: a
	// token is used at once, possibly on another one than signed it, whose
	// clock may be a few milliseconds behind its not-before time.
	clockSkewLeeway = 5 * time.Second
)

func init() {
	// Issue times carry milliseconds ("iat": 1761187200.123, still a valid
	// NumericDate). Signing out everywhere rejects every token issued
	// before that instant; at whole-second precision a token issued
	// earlier in the same second would survive it.
	jwt.TimePrecision = time.Millisecond
}

// publishedSecrets are values that have appeared in this repository as a
// default or placeholder. Anyone who can read the repo can sign tokens
// with them, so they are rejected even though they pass the length check.
var publishedSecrets = map[string]struct{}{
	"tuai-development-secret-change-in-production":                 {},
	"your-super-secret-key-change-in-production-min-32-characters": {},
}

// ValidateSecret checks JWT_SECRET at application startup.
//
// The check applies in every environment. A token signed with a missing,
// short, or published secret can be forged, whatever ENV says.
func ValidateSecret() error {
	_, err := GetSecret()
	return err
}

// GetSecret returns the JWT signing key from JWT_SECRET. There is no
// fallback: signing or verifying with an invalid secret is an error.
func GetSecret() ([]byte, error) {
	secret := os.Getenv("JWT_SECRET")

	if secret == "" {
		return nil, fmt.Errorf("%w: JWT_SECRET environment variable must be set", ErrInvalidJWTSecret)
	}

	if len(secret) < MinSecretLength {
		return nil, fmt.Errorf("%w: JWT_SECRET must be at least %d characters long (current: %d)",
			ErrInvalidJWTSecret, MinSecretLength, len(secret))
	}

	if _, ok := publishedSecrets[secret]; ok {
		return nil, fmt.Errorf("%w: JWT_SECRET is a placeholder published in this repository; generate one with `openssl rand -base64 48`",
			ErrInvalidJWTSecret)
	}

	return []byte(secret), nil
}

// GetExpirationTime returns the access token lifetime from JWT_EXPIRATION.
// The value is trimmed: `make` keeps the spaces before an inline .env
// comment as part of it.
func GetExpirationTime() time.Duration {
	duration, err := time.ParseDuration(strings.TrimSpace(os.Getenv("JWT_EXPIRATION")))
	if err != nil || duration <= 0 {
		return DefaultExpiration
	}
	return duration
}

// GenerateToken generates a new JWT token with user claims, belonging to
// the sign-in session sessionID.
//
// Note: the user's permission set is NOT embedded in the token. Keeping
// it out lets the token stay small enough to survive every proxy in the
// chain. The authz package (internal/shared/authz) handles runtime
// permission lookup against Redis.
func GenerateToken(
	userID, email, username, fullName string,
	isSuperAdmin bool,
	roles []string,
	sessionID string,
) (string, error) {
	now := time.Now()

	claims := &Claims{
		UserID:       userID,
		Email:        email,
		Username:     username,
		FullName:     fullName,
		IsSuperAdmin: isSuperAdmin,
		Roles:        roles,
		SessionID:    sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(GetExpirationTime())),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    Issuer,
			Subject:   userID,
		},
	}

	secret, err := GetSecret()
	if err != nil {
		return "", err
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, nil
}

// ParseToken parses and validates a JWT token string. It accepts only
// HS256 tokens issued by this API that carry an expiry and an issue time.
func ParseToken(tokenString string) (*Claims, error) {
	if tokenString == "" {
		return nil, ErrTokenNotProvided
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{},
		func(*jwt.Token) (any, error) {
			return GetSecret()
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(Issuer),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(clockSkewLeeway),
	)

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		if errors.Is(err, jwt.ErrSignatureInvalid) {
			return nil, ErrInvalidSignature
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid || claims.IssuedAt == nil {
		return nil, ErrInvalidToken
	}

	return claims, nil
}
