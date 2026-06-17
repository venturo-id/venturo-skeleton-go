package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go/internal/middleware"
	"venturo-skeleton-go/internal/modules/core/branch/dto"
	"venturo-skeleton-go/internal/modules/core/branch/service"
	"venturo-skeleton-go/internal/shared/response"
)

type UserBranchHandler struct {
	svc *service.UserBranchService
}

func NewUserBranchHandler(svc *service.UserBranchService) *UserBranchHandler {
	return &UserBranchHandler{svc: svc}
}

// resolveBranchScope returns the scope company ID (or nil for super admin).
// Non-super-admin callers must have a company context; otherwise the
// endpoint 403s.
func (h *UserBranchHandler) resolveBranchScope(c *gin.Context) (*string, bool) {
	claims, _ := middleware.GetUserFromContext(c)
	if claims != nil && claims.HasRole("super_admin") {
		return nil, true
	}
	companyID := middleware.GetCompanyID(c)
	if companyID == "" {
		response.Error(c, http.StatusForbidden, "Company context required", "")
		return nil, false
	}
	return &companyID, true
}

func (h *UserBranchHandler) GetUserBranches(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		response.Error(c, http.StatusBadRequest, "User ID is required", "")
		return
	}

	ctx := c.Request.Context()
	ids, err := h.svc.GetUserBranchIDs(ctx, userID)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "User branches retrieved successfully", ids)
}

func (h *UserBranchHandler) SyncUserBranches(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		response.Error(c, http.StatusBadRequest, "User ID is required", "")
		return
	}

	var req dto.SyncUserBranchesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	scope, ok := h.resolveBranchScope(c)
	if !ok {
		return
	}

	updatedBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	if err := h.svc.SyncUserBranches(ctx, userID, req.BranchIDs, updatedBy, scope); err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "User branches synced successfully", nil)
}
