package middleware

import (
	"github.com/gin-gonic/gin"

	"timebox-be/internal/shared/audit"
)

// AuditClient puts the client's IP address and user agent into the
// request context, where the audit log reads them. JWTAuth adds the
// caller.
func AuditClient() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(audit.WithClient(c.Request.Context(), c.ClientIP(), c.Request.UserAgent()))
		c.Next()
	}
}
