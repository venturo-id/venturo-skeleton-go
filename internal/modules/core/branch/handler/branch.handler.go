package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"venturo-skeleton-go/internal/middleware"
	"venturo-skeleton-go/internal/modules/core/branch/dto"
	"venturo-skeleton-go/internal/modules/core/branch/service"
	"venturo-skeleton-go/internal/shared/response"
)

type BranchHandler struct {
	branchService *service.BranchService
}

func NewBranchHandler(branchService *service.BranchService) *BranchHandler {
	return &BranchHandler{branchService: branchService}
}

func (h *BranchHandler) GetAll(c *gin.Context) {
	var params dto.BranchQueryParams
	if err := c.ShouldBindQuery(&params); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid query parameters", "")
		return
	}

	companyID := middleware.GetCompanyID(c)
	params.ScopeBranchIDs = middleware.GetAllowedBranchIDs(c)
	ctx := c.Request.Context()

	result, err := h.branchService.GetAll(ctx, companyID, &params)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.SuccessWithPagination(c, http.StatusOK, "Branches retrieved successfully",
		result.Branches, result.Page, result.Limit, result.Total)
}

func (h *BranchHandler) GetAllByCompanies(c *gin.Context) {
	var params dto.BranchQueryParams
	if err := c.ShouldBindQuery(&params); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid query parameters", "")
		return
	}

	// company_ids arrives as either a CSV string ("uuid1,uuid2") or
	// repeated params (?company_ids=uuid1&company_ids=uuid2). Gin cannot
	// auto-split CSV into a []string, so do it here and validate each
	// element as a UUID ourselves.
	if params.CompanyIDsRaw != "" {
		parts := strings.Split(params.CompanyIDsRaw, ",")
		ids := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, err := uuid.Parse(p); err != nil {
				response.Error(c, http.StatusBadRequest, "Invalid company_ids", "each company_ids entry must be a UUID")
				return
			}
			ids = append(ids, p)
		}
		params.CompanyIDs = ids
	}

	claims, _ := middleware.GetUserFromContext(c)
	if claims == nil {
		response.Error(c, http.StatusUnauthorized, "Unauthorized", "")
		return
	}
	isSuperAdmin := claims.HasRole("super_admin")
	params.ScopeBranchIDs = middleware.GetAllowedBranchIDs(c)

	ctx := c.Request.Context()
	result, err := h.branchService.GetAllByCompanies(ctx, claims.UserID, isSuperAdmin, &params)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.SuccessWithPagination(c, http.StatusOK, "Branches retrieved successfully",
		result.Branches, result.Page, result.Limit, result.Total)
}

func (h *BranchHandler) GetByID(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Branch ID is required", "")
		return
	}

	ctx := c.Request.Context()
	result, err := h.branchService.GetByID(ctx, id)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	// Hide branches the caller has no access to behind a 404 (matches tenant
	// visibility semantics: never leak existence).
	if !middleware.IsBranchAllowed(c, result.ID) {
		response.Error(c, http.StatusNotFound, "Branch not found", "")
		return
	}

	response.Success(c, http.StatusOK, "Branch retrieved successfully", result)
}

func (h *BranchHandler) Create(c *gin.Context) {
	var req dto.CreateBranchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	companyID := middleware.GetCompanyID(c)
	createdBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	result, err := h.branchService.Create(ctx, companyID, &req, createdBy)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusCreated, "Branch created successfully", result)
}

func (h *BranchHandler) Update(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Branch ID is required", "")
		return
	}

	// Block updates to branches outside the caller's branch scope.
	if !middleware.IsBranchAllowed(c, id) {
		response.Error(c, http.StatusNotFound, "Branch not found", "")
		return
	}

	var req dto.UpdateBranchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	updatedBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	result, err := h.branchService.Update(ctx, id, &req, updatedBy)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "Branch updated successfully", result)
}

func (h *BranchHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Branch ID is required", "")
		return
	}

	if !middleware.IsBranchAllowed(c, id) {
		response.Error(c, http.StatusNotFound, "Branch not found", "")
		return
	}

	deletedBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	err := h.branchService.Delete(ctx, id, deletedBy)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "Branch deleted successfully", nil)
}
