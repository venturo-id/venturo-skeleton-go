package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go/internal/middleware"
	"venturo-skeleton-go/internal/modules/core/approval/dto"
	"venturo-skeleton-go/internal/modules/core/approval/service"
	"venturo-skeleton-go/internal/shared/response"
)

type ApprovalConfigHandler struct {
	svc *service.ApprovalConfigService
}

func NewApprovalConfigHandler(svc *service.ApprovalConfigService) *ApprovalConfigHandler {
	return &ApprovalConfigHandler{svc: svc}
}

func (h *ApprovalConfigHandler) GetAll(c *gin.Context) {
	var params dto.ApprovalConfigQueryParams
	if err := c.ShouldBindQuery(&params); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid query parameters", "")
		return
	}

	companyID := middleware.GetCompanyID(c)
	result, err := h.svc.GetAll(c.Request.Context(), companyID, &params)
	if err != nil {
		response.RenderError(c, err)
		return
	}
	response.Success(c, http.StatusOK, "Approval configs retrieved successfully", result)
}

func (h *ApprovalConfigHandler) GetByID(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Config ID is required", "")
		return
	}

	companyID := middleware.GetCompanyID(c)
	result, err := h.svc.GetByID(c.Request.Context(), id, companyID)
	if err != nil {
		response.RenderError(c, err)
		return
	}
	response.Success(c, http.StatusOK, "Approval config retrieved successfully", result)
}

func (h *ApprovalConfigHandler) Create(c *gin.Context) {
	var req dto.CreateApprovalConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	companyID := middleware.GetCompanyID(c)
	createdBy := middleware.MustGetUserID(c)

	result, err := h.svc.Create(c.Request.Context(), companyID, &req, createdBy)
	if err != nil {
		response.RenderError(c, err)
		return
	}
	response.Success(c, http.StatusCreated, "Approval config created successfully", result)
}

func (h *ApprovalConfigHandler) Update(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Config ID is required", "")
		return
	}

	var req dto.UpdateApprovalConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	companyID := middleware.GetCompanyID(c)
	updatedBy := middleware.MustGetUserID(c)

	result, err := h.svc.Update(c.Request.Context(), id, companyID, &req, updatedBy)
	if err != nil {
		response.RenderError(c, err)
		return
	}
	response.Success(c, http.StatusOK, "Approval config updated successfully", result)
}

func (h *ApprovalConfigHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Config ID is required", "")
		return
	}

	companyID := middleware.GetCompanyID(c)
	deletedBy := middleware.MustGetUserID(c)

	if err := h.svc.Delete(c.Request.Context(), id, companyID, deletedBy); err != nil {
		response.RenderError(c, err)
		return
	}
	response.Success(c, http.StatusOK, "Approval config deleted successfully", nil)
}
