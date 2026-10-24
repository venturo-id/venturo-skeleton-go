// Package auditlog serves the audit log for reading. Entries are written
// by the other modules through internal/shared/audit.
package auditlog

import (
	"github.com/gin-gonic/gin"

	"timebox-be/internal/middleware"
	"timebox-be/internal/modules/core/auditlog/handler"
	"timebox-be/internal/modules/core/auditlog/service"
	"timebox-be/internal/shared/audit"
)

type AuditLogModule struct {
	Handler *handler.AuditLogHandler
}

// Initialize wires the module on the log's reader.
func Initialize(reader audit.Reader) *AuditLogModule {
	return &AuditLogModule{Handler: handler.NewAuditLogHandler(service.NewAuditLogService(reader))}
}

// SetupRoutes registers GET /audit-logs.
func (m *AuditLogModule) SetupRoutes(router *gin.RouterGroup) {
	router.GET("/audit-logs", middleware.JWTAuth(), middleware.RequirePermission("audit_logs:read"), m.Handler.List)
}
