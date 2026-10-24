package middleware

import (
	"net/http"
	"strings"

	"timebox-be/internal/shared/authz"
	"timebox-be/internal/shared/response"
	"timebox-be/pkg/logger"

	"github.com/gin-gonic/gin"
)

// RequirePermission checks if user has a specific permission.
//
// It evaluates the caller's live authz subject, loaded by JWTAuth: for a
// JWT request the user's current roles (super admin bypasses), for an
// API-key request the key's effective scope (never super admin). Nothing
// is read from the token's claims, so role changes and deactivation take
// effect as soon as the authz cache is invalidated.
//
// Must be used after JWTAuth middleware.
func RequirePermission(permission string) gin.HandlerFunc {
	return requireSubject(permission, func(s authz.Subject) bool {
		return s.Has(permission)
	})
}

// RequireAnyPermission checks if user has at least one of the specified permissions
// Must be used after JWTAuth middleware
func RequireAnyPermission(permissions ...string) gin.HandlerFunc {
	return requireSubject(strings.Join(permissions, ", "), func(s authz.Subject) bool {
		for _, need := range permissions {
			if s.Has(need) {
				return true
			}
		}
		return false
	})
}

// RequireAllPermissions checks if user has all the specified permissions
// Must be used after JWTAuth middleware
func RequireAllPermissions(permissions ...string) gin.HandlerFunc {
	return requireSubject(strings.Join(permissions, ", "), func(s authz.Subject) bool {
		return s.Covers(permissions)
	})
}

// requireSubject rejects the request with 403 unless check passes for
// the caller's subject. required is only used for logging.
func requireSubject(required string, check func(authz.Subject) bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := GetSubject(c)
		if !ok {
			logger.Warn("User not authenticated for permission check")
			response.Error(c, http.StatusUnauthorized, "Unauthorized", "Authentication required")
			c.Abort()
			return
		}

		if !check(subject) {
			userID, _ := GetUserID(c)
			logger.Warn("User lacks required permission",
				logger.String("user_id", userID),
				logger.String("required_permission", required),
			)
			response.Error(c, http.StatusForbidden, "Forbidden", "Insufficient permissions")
			c.Abort()
			return
		}

		c.Next()
	}
}
