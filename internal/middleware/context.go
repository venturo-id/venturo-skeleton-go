package middleware

import (
	"errors"

	"timebox-be/internal/shared/authz"
	jwtpkg "timebox-be/pkg/jwt"

	"github.com/gin-gonic/gin"
)

const (
	// UserContextKey is the key used to store user claims in gin.Context
	UserContextKey = "user"
	// AuthTypeKey is the key used to store the authentication type
	AuthTypeKey = "auth_type"
	// APIKeyIDKey is the key used to store the API key ID when using API key auth
	APIKeyIDKey = "api_key_id"
	// SubjectKey is the key used to store the caller's authz.Subject
	SubjectKey = "authz_subject"
)

// Authentication types stored under AuthTypeKey.
const (
	AuthTypeJWT    = "jwt"
	AuthTypeAPIKey = "api_key"
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

// MustGetUserID retrieves user ID from context and panics if not found
// Use this only in handlers that are protected by JWTAuth middleware
func MustGetUserID(c *gin.Context) string {
	return MustGetUserFromContext(c).UserID
}

// SetAuthType stores the authentication type in gin.Context
func SetAuthType(c *gin.Context, authType string) {
	c.Set(AuthTypeKey, authType)
}

// GetAuthType returns AuthTypeJWT or AuthTypeAPIKey, or "" when JWTAuth
// has not run.
func GetAuthType(c *gin.Context) string {
	return c.GetString(AuthTypeKey)
}

// SetAPIKeyID stores the API key ID in gin.Context
func SetAPIKeyID(c *gin.Context, keyID string) {
	c.Set(APIKeyIDKey, keyID)
}

// GetAPIKeyID retrieves the API key ID from context
func GetAPIKeyID(c *gin.Context) string {
	return c.GetString(APIKeyIDKey)
}

// IsAPIKeyAuth returns true if the current request is authenticated via API key
func IsAPIKeyAuth(c *gin.Context) bool {
	return GetAuthType(c) == AuthTypeAPIKey
}

// SetSubject stores the caller's live authorization state in gin.Context.
func SetSubject(c *gin.Context, subject authz.Subject) {
	c.Set(SubjectKey, subject)
}

// GetSubject retrieves the caller's live authorization state. For an
// API-key request it is the key's effective scope, never super admin.
// ok is false when JWTAuth has not run.
func GetSubject(c *gin.Context) (authz.Subject, bool) {
	value, exists := c.Get(SubjectKey)
	if !exists {
		return authz.Subject{}, false
	}
	subject, ok := value.(authz.Subject)
	return subject, ok
}

// Caller returns the caller's live authorization state for services that
// apply the grant ceiling (Subject.Covers). Without JWTAuth it is the
// zero value: inactive, holding nothing.
func Caller(c *gin.Context) authz.Subject {
	subject, _ := GetSubject(c)
	return subject
}

// IsSuperAdmin reports whether the caller holds an active super_admin
// role right now. It reads the live subject, never the token's
// is_super_admin claim, which stays set until the token expires.
func IsSuperAdmin(c *gin.Context) bool {
	return Caller(c).SuperAdmin
}
