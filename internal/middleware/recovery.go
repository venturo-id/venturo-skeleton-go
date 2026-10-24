package middleware

import (
	"fmt"
	"io"
	"net/http"

	"timebox-be/internal/shared/response"
	"timebox-be/pkg/logger"

	"github.com/gin-gonic/gin"
)

// Recovery turns a panic in a handler into a 500. It logs the panic, the
// route and the client IP (with a stack trace, as every error-level
// entry), but never the request headers or body: they carry bearer
// tokens, API keys and passwords, and error logs may be forwarded to
// Discord.
func Recovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, recovered any) {
		logger.Error("Panic recovered",
			logger.String("panic", fmt.Sprint(recovered)),
			logger.String("method", c.Request.Method),
			logger.String("path", c.FullPath()),
			logger.String("ip", c.ClientIP()),
		)
		response.Error(c, http.StatusInternalServerError, "Internal server error", "")
		c.Abort()
	})
}

// MaxBodySize caps request bodies at limit bytes. Reading past it fails,
// and the request helpers answer 413.
func MaxBodySize(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}
