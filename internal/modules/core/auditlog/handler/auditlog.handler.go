package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"timebox-be/internal/modules/core/auditlog/dto"
	"timebox-be/internal/modules/core/auditlog/service"
	"timebox-be/internal/shared/audit"
	"timebox-be/internal/shared/request"
	"timebox-be/internal/shared/response"
)

type AuditLogHandler struct {
	service *service.AuditLogService
}

func NewAuditLogHandler(service *service.AuditLogService) *AuditLogHandler {
	return &AuditLogHandler{service: service}
}

// List handles GET /core/v1/audit-logs.
func (h *AuditLogHandler) List(c *gin.Context) {
	var params dto.AuditLogQueryParams
	if !request.BindQuery(c, &params) {
		return
	}

	entries, total, err := h.service.List(c.Request.Context(), &params)
	if err != nil {
		if errors.Is(err, audit.ErrDisabled) {
			response.Error(c, http.StatusServiceUnavailable, "Audit log is not configured", "")
			return
		}
		response.InternalError(c, "Failed to read the audit log", err)
		return
	}

	response.SuccessWithPagination(c, http.StatusOK, "Audit log retrieved successfully",
		entries, params.Page, params.Limit, total)
}
