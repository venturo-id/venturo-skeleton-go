package middleware

import (
	"errors"

	jwtpkg "venturo-skeleton-go-simple/pkg/jwt"

	"github.com/gin-gonic/gin"
)

const (
	// UserContextKey is the key used to store user claims in gin.Context
	UserContextKey = "user"
	// AuthTypeKey is the key used to store the authentication type (jwt or api_key)
	AuthTypeKey = "auth_type"
	// ApiKeyIDKey is the key used to store the API key ID when using API key auth
	ApiKeyIDKey = "api_key_id"
)

var (
	ErrUserNotFoundInContext = errors.New("user not found in context")
)

// SetUserContext stores user claims in gin.Context
func SetUserContext(c *gin.Context, claims *jwtpkg.Claims) {
	c.Set(UserContextKey, claims)
}

// GetUserFromContext retrieves user claims from gin.Context
func GetUserFromContext(c *gin.Context) (*jwtpkg.Claims, error) {
	value, exists := c.Get(UserContextKey)
	if !exists {
		return nil, ErrUserNotFoundInContext
	}

	claims, ok := value.(*jwtpkg.Claims)
	if !ok {
		return nil, ErrUserNotFoundInContext
	}

	return claims, nil
}

// MustGetUserFromContext retrieves user claims and panics if not found
// Use this only in handlers that are protected by JWTAuth middleware
func MustGetUserFromContext(c *gin.Context) *jwtpkg.Claims {
	claims, err := GetUserFromContext(c)
	if err != nil {
		panic(err)
	}
	return claims
}

// GetUserID retrieves user ID from context
func GetUserID(c *gin.Context) (string, error) {
	claims, err := GetUserFromContext(c)
	if err != nil {
		return "", err
	}
	return claims.UserID, nil
}

// GetUserEmail retrieves user email from context
func GetUserEmail(c *gin.Context) (string, error) {
	claims, err := GetUserFromContext(c)
	if err != nil {
		return "", err
	}
	return claims.Email, nil
}

// GetUserRoles retrieves user roles from context
func GetUserRoles(c *gin.Context) ([]string, error) {
	claims, err := GetUserFromContext(c)
	if err != nil {
		return nil, err
	}
	return claims.Roles, nil
}

// MustGetUserID retrieves user ID from context and panics if not found
// Use this only in handlers that are protected by JWTAuth middleware
func MustGetUserID(c *gin.Context) string {
	claims := MustGetUserFromContext(c)
	return claims.UserID
}

// SetAuthType stores the authentication type in gin.Context
func SetAuthType(c *gin.Context, authType string) {
	c.Set(AuthTypeKey, authType)
}

// GetAuthType retrieves the authentication type from context
// Returns "jwt" or "api_key"
func GetAuthType(c *gin.Context) string {
	authType, exists := c.Get(AuthTypeKey)
	if !exists {
		return "jwt"
	}
	if t, ok := authType.(string); ok {
		return t
	}
	return "jwt"
}

// SetApiKeyID stores the API key ID in gin.Context
func SetApiKeyID(c *gin.Context, keyID string) {
	c.Set(ApiKeyIDKey, keyID)
}

// GetApiKeyID retrieves the API key ID from context
func GetApiKeyID(c *gin.Context) string {
	keyID, exists := c.Get(ApiKeyIDKey)
	if !exists {
		return ""
	}
	if k, ok := keyID.(string); ok {
		return k
	}
	return ""
}

// IsApiKeyAuth returns true if the current request is authenticated via API key
func IsApiKeyAuth(c *gin.Context) bool {
	return GetAuthType(c) == "api_key"
}
