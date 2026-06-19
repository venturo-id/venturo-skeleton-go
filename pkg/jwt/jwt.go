package jwt

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
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
)

// ValidateSecret validates that JWT_SECRET is set and meets minimum requirements
// This should be called at application startup
func ValidateSecret(env string) error {
	secret := os.Getenv("JWT_SECRET")

	// In production, JWT_SECRET must be set
	if env == "production" {
		if secret == "" {
			return fmt.Errorf("%w: JWT_SECRET environment variable must be set in production", ErrInvalidJWTSecret)
		}

		if len(secret) < MinSecretLength {
			return fmt.Errorf("%w: JWT_SECRET must be at least %d characters long (current: %d)",
				ErrInvalidJWTSecret, MinSecretLength, len(secret))
		}

		// Check if using the default development secret
		if secret == "tuai-development-secret-change-in-production" {
			return fmt.Errorf("%w: cannot use default development secret in production", ErrInvalidJWTSecret)
		}
	} else {
		// In development/staging, warn if secret is weak but don't fail
		if secret != "" && len(secret) < MinSecretLength {
			fmt.Printf("WARNING: JWT_SECRET is shorter than recommended minimum of %d characters\n", MinSecretLength)
		}
	}

	return nil
}

// GetSecret returns JWT secret from environment variable
func GetSecret() []byte {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		// Default secret for development only
		// In production, JWT_SECRET MUST be set in .env
		secret = "tuai-development-secret-change-in-production"
	}
	return []byte(secret)
}

// GetExpirationTime returns token expiration duration from environment
func GetExpirationTime() time.Duration {
	expStr := os.Getenv("JWT_EXPIRATION")
	if expStr == "" {
		return 24 * time.Hour // Default 24 hours
	}

	duration, err := time.ParseDuration(expStr)
	if err != nil {
		return 24 * time.Hour // Fallback to 24 hours
	}

	return duration
}

// GetRefreshExpirationTime returns the refresh-token lifetime from the
// environment. Mirrors GetExpirationTime: reads JWT_REFRESH_EXPIRATION,
// defaults to 168h (7 days), and falls back to the same default on a
// parse error. Lets operators tune session length per environment
// without recompiling.
func GetRefreshExpirationTime() time.Duration {
	expStr := os.Getenv("JWT_REFRESH_EXPIRATION")
	if expStr == "" {
		return 168 * time.Hour // Default 7 days
	}

	duration, err := time.ParseDuration(expStr)
	if err != nil {
		return 168 * time.Hour // Fallback to 7 days
	}

	return duration
}

// GenerateToken generates a new JWT token with user claims.
// clientID / clientSlug are the registration-level tenant identifiers —
// callers pass them in so the FE can read its current client from the
// JWT (useful for per-client features like translation overrides).
// Both are optional: pre-company-switch tokens (refresh with no company
// context) should pass "" for both.
//
// Note: the user's permission set is NOT embedded in the token. Keeping
// it out lets the token stay small enough to survive every proxy in the
// chain. The authz package (internal/shared/authz) handles runtime
// permission lookup against Redis.
func GenerateToken(
	userID, companyID, companyName, clientID, clientSlug, email, username string,
	fullName string,
	isSuperAdmin bool,
	roles []string,
) (string, error) {
	now := time.Now()
	expirationTime := GetExpirationTime()

	claims := &Claims{
		UserID:       userID,
		CompanyID:    companyID,
		CompanyName:  companyName,
		ClientID:     clientID,
		ClientSlug:   clientSlug,
		Email:        email,
		Username:     username,
		FullName:     fullName,
		IsSuperAdmin: isSuperAdmin,
		Roles:        roles,
		RegisteredClaims: jwt.RegisteredClaims{
			// ID is the per-token jti — a unique UUID minted for every
			// access token. It lets Logout add this exact token to the
			// Redis denylist (see internal/shared/tokendenylist) so a
			// stolen/cached token stops working immediately on logout
			// instead of lingering until natural expiry. IssuedAt (iat)
			// is the cutoff anchor used by LogoutAll to reject every
			// token minted before the "logout everywhere" moment.
			ID:        uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(now.Add(expirationTime)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "tuai-api",
			Subject:   userID,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(GetSecret())
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, nil
}

// ParseToken parses and validates a JWT token string
func ParseToken(tokenString string) (*Claims, error) {
	if tokenString == "" {
		return nil, ErrTokenNotProvided
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return GetSecret(), nil
	})

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
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// ValidateToken validates a token and returns claims if valid
func ValidateToken(tokenString string) (*Claims, error) {
	return ParseToken(tokenString)
}

// RefreshToken generates a new token from an existing valid token
// This can be used to extend user session
func RefreshToken(oldTokenString string) (string, error) {
	claims, err := ParseToken(oldTokenString)
	if err != nil {
		// Allow refresh even if token is expired (but not if invalid signature)
		if !errors.Is(err, ErrExpiredToken) {
			return "", err
		}
	}

	// Generate new token with same claims. Permissions are not part of
	// the token anymore — the caller refetches them from authz on the
	// next request, so there's nothing to carry across here.
	return GenerateToken(
		claims.UserID,
		claims.CompanyID,
		claims.CompanyName,
		claims.ClientID,
		claims.ClientSlug,
		claims.Email,
		claims.Username,
		claims.FullName,
		claims.IsSuperAdmin,
		claims.Roles,
	)
}
