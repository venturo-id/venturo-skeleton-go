package middleware

import (
	"net/http"

	"venturo-skeleton-go/internal/shared/authz"
	"venturo-skeleton-go/internal/shared/response"
	"venturo-skeleton-go/pkg/logger"

	"github.com/gin-gonic/gin"
)

// authzService is the permission-cache backend used by RequirePermission
// and friends. It is wired in at bootstrap via SetAuthzService; until
// then, any permission-gated request is rejected with 500 rather than
// silently passing.
var authzService *authz.Service

// SetAuthzService registers the permission-lookup backend. Call this
// once during router setup, after Redis is initialised.
func SetAuthzService(s *authz.Service) {
	authzService = s
}

// RequireRole checks if user has at least one of the required roles
// Must be used after JWTAuth middleware
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := GetUserFromContext(c)
		if err != nil {
			logger.Warn("User not authenticated for role check")
			response.Error(c, http.StatusUnauthorized, "Unauthorized", "Authentication required")
			c.Abort()
			return
		}

		// Check if user has any of the required roles
		if !claims.HasAnyRole(roles...) {
			logger.Warn("User lacks required role",
				logger.String("user_id", claims.UserID),
				logger.String("required_roles", joinStrings(roles)),
				logger.String("user_roles", joinStrings(claims.Roles)),
			)
			response.Error(c, http.StatusForbidden, "Forbidden", "Insufficient permissions")
			c.Abort()
			return
		}

		logger.Info("Role check passed",
			logger.String("user_id", claims.UserID),
			logger.String("matched_role", findMatchingRole(claims.Roles, roles)),
		)

		c.Next()
	}
}

// RequireAllRoles checks if user has all the specified roles
// Must be used after JWTAuth middleware
func RequireAllRoles(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := GetUserFromContext(c)
		if err != nil {
			logger.Warn("User not authenticated for role check")
			response.Error(c, http.StatusUnauthorized, "Unauthorized", "Authentication required")
			c.Abort()
			return
		}

		// Check if user has all required roles
		if !claims.HasAllRoles(roles...) {
			logger.Warn("User lacks all required roles",
				logger.String("user_id", claims.UserID),
				logger.String("required_roles", joinStrings(roles)),
				logger.String("user_roles", joinStrings(claims.Roles)),
			)
			response.Error(c, http.StatusForbidden, "Forbidden", "Insufficient permissions")
			c.Abort()
			return
		}

		logger.Info("All roles check passed",
			logger.String("user_id", claims.UserID),
		)

		c.Next()
	}
}

// RequirePermission checks if user has a specific permission.
//
// For JWT-authenticated requests the effective permission set is looked
// up from Redis (via authz.Service) on each request — permissions are
// no longer embedded in the token.
//
// For API-key-authenticated requests the set is pinned on
// claims.ScopedPermissions at auth time (API keys may carry a scope
// narrower than the user's full permission set, so they must not
// consult the user-level Redis cache).
//
// Must be used after JWTAuth middleware.
func RequirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := GetUserFromContext(c)
		if err != nil {
			logger.Warn("User not authenticated for permission check")
			response.Error(c, http.StatusUnauthorized, "Unauthorized", "Authentication required")
			c.Abort()
			return
		}

		// Super admin bypasses all permission checks
		if claims.IsSuperAdmin {
			c.Next()
			return
		}

		// API-key flow: use the pre-resolved, possibly-narrowed set
		// instead of the user-level cache.
		if claims.ScopedPermissions != nil {
			if claims.HasScopedPermission(permission) {
				c.Next()
				return
			}
			response.Error(c, http.StatusForbidden, "Forbidden", "Insufficient permissions")
			c.Abort()
			return
		}

		if authzService == nil {
			logger.Error("authz service not initialised — cannot evaluate permission")
			response.Error(c, http.StatusInternalServerError, "Server misconfiguration", "Permission backend unavailable")
			c.Abort()
			return
		}

		ok, err := authzService.Has(c.Request.Context(), claims.UserID, claims.CompanyID, permission)
		if err != nil {
			logger.Error("Failed to evaluate permission",
				logger.String("user_id", claims.UserID),
				logger.String("permission", permission),
				logger.Err(err),
			)
			response.Error(c, http.StatusInternalServerError, "Permission lookup failed", "")
			c.Abort()
			return
		}
		if !ok {
			logger.Warn("User lacks required permission",
				logger.String("user_id", claims.UserID),
				logger.String("required_permission", permission),
			)
			response.Error(c, http.StatusForbidden, "Forbidden", "Insufficient permissions")
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAnyPermission checks if user has at least one of the specified permissions
// Must be used after JWTAuth middleware
func RequireAnyPermission(permissions ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := GetUserFromContext(c)
		if err != nil {
			logger.Warn("User not authenticated for permission check")
			response.Error(c, http.StatusUnauthorized, "Unauthorized", "Authentication required")
			c.Abort()
			return
		}

		// Super admin bypasses all permission checks
		if claims.IsSuperAdmin {
			c.Next()
			return
		}

		// API-key flow: evaluate against the pinned scoped set.
		if claims.ScopedPermissions != nil {
			for _, need := range permissions {
				if claims.HasScopedPermission(need) {
					c.Next()
					return
				}
			}
			response.Error(c, http.StatusForbidden, "Forbidden", "Insufficient permissions")
			c.Abort()
			return
		}

		if authzService == nil {
			logger.Error("authz service not initialised — cannot evaluate permission")
			response.Error(c, http.StatusInternalServerError, "Server misconfiguration", "Permission backend unavailable")
			c.Abort()
			return
		}

		ok, err := authzService.HasAny(c.Request.Context(), claims.UserID, claims.CompanyID, permissions...)
		if err != nil {
			logger.Error("Failed to evaluate permissions",
				logger.String("user_id", claims.UserID),
				logger.Err(err),
			)
			response.Error(c, http.StatusInternalServerError, "Permission lookup failed", "")
			c.Abort()
			return
		}
		if !ok {
			logger.Warn("User lacks required permissions",
				logger.String("user_id", claims.UserID),
				logger.String("required_permissions", joinStrings(permissions)),
			)
			response.Error(c, http.StatusForbidden, "Forbidden", "Insufficient permissions")
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAllPermissions checks if user has all the specified permissions
// Must be used after JWTAuth middleware
func RequireAllPermissions(permissions ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := GetUserFromContext(c)
		if err != nil {
			logger.Warn("User not authenticated for permission check")
			response.Error(c, http.StatusUnauthorized, "Unauthorized", "Authentication required")
			c.Abort()
			return
		}

		// Super admin bypasses all permission checks
		if claims.IsSuperAdmin {
			c.Next()
			return
		}

		// API-key flow: every required perm must be in the pinned set.
		if claims.ScopedPermissions != nil {
			for _, need := range permissions {
				if !claims.HasScopedPermission(need) {
					response.Error(c, http.StatusForbidden, "Forbidden", "Insufficient permissions")
					c.Abort()
					return
				}
			}
			c.Next()
			return
		}

		if authzService == nil {
			logger.Error("authz service not initialised — cannot evaluate permission")
			response.Error(c, http.StatusInternalServerError, "Server misconfiguration", "Permission backend unavailable")
			c.Abort()
			return
		}

		ok, err := authzService.HasAll(c.Request.Context(), claims.UserID, claims.CompanyID, permissions...)
		if err != nil {
			logger.Error("Failed to evaluate permissions",
				logger.String("user_id", claims.UserID),
				logger.Err(err),
			)
			response.Error(c, http.StatusInternalServerError, "Permission lookup failed", "")
			c.Abort()
			return
		}
		if !ok {
			logger.Warn("User lacks all required permissions",
				logger.String("user_id", claims.UserID),
				logger.String("required_permissions", joinStrings(permissions)),
			)
			response.Error(c, http.StatusForbidden, "Forbidden", "Insufficient permissions")
			c.Abort()
			return
		}

		c.Next()
	}
}

// Helper functions

// joinStrings joins string slice with comma separator
func joinStrings(strs []string) string {
	result := ""
	for i, str := range strs {
		if i > 0 {
			result += ", "
		}
		result += str
	}
	return result
}

// findMatchingRole finds the first matching role between user roles and required roles
func findMatchingRole(userRoles, requiredRoles []string) string {
	for _, userRole := range userRoles {
		for _, reqRole := range requiredRoles {
			if userRole == reqRole {
				return userRole
			}
		}
	}
	return ""
}
