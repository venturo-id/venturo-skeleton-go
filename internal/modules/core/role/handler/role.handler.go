package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go/internal/middleware"
	"venturo-skeleton-go/internal/modules/core/role/dto"
	"venturo-skeleton-go/internal/modules/core/role/service"
	"venturo-skeleton-go/internal/shared/response"
)

type RoleHandler struct {
	roleService *service.RoleService
}

func NewRoleHandler(roleService *service.RoleService) *RoleHandler {
	return &RoleHandler{
		roleService: roleService,
	}
}

func (h *RoleHandler) GetAll(c *gin.Context) {
	var params dto.RoleQueryParams
	if err := c.ShouldBindQuery(&params); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid query parameters", "")
		return
	}

	claims, _ := middleware.GetUserFromContext(c)
	isSuperAdmin := claims != nil && claims.HasRole("super_admin")

	// Non-super admin: force scope to their own company and include the global
	// "administrator" system role (read-only). Hide "super_admin".
	if !isSuperAdmin {
		companyID := middleware.GetCompanyID(c)
		if companyID != "" {
			params.CompanyID = &companyID
			includeGlobal := true
			params.IncludeGlobal = &includeGlobal
		}
		params.ExcludeCodes = []string{"super_admin"}
	}

	ctx := c.Request.Context()
	result, err := h.roleService.GetAll(ctx, &params)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.SuccessWithPagination(c, http.StatusOK, "Roles retrieved successfully",
		result.Roles, result.Page, result.Limit, result.Total)
}

func (h *RoleHandler) GetByID(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Role ID is required", "")
		return
	}

	ctx := c.Request.Context()
	result, err := h.roleService.GetByID(ctx, id)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "Role retrieved successfully", result)
}

func (h *RoleHandler) Create(c *gin.Context) {
	var req dto.CreateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	// Force company scope from JWT context. Non-super-admin users can only
	// create roles within their current company. Super admin has no company
	// context, so the created role becomes global (company_id IS NULL).
	companyID := middleware.GetCompanyID(c)
	if companyID != "" {
		req.CompanyID = &companyID
	} else {
		req.CompanyID = nil
	}

	createdBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	result, err := h.roleService.Create(ctx, &req, createdBy)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusCreated, "Role created successfully", result)
}

func (h *RoleHandler) Update(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Role ID is required", "")
		return
	}

	var req dto.UpdateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	updatedBy := middleware.MustGetUserID(c)
	claims, _ := middleware.GetUserFromContext(c)
	isSuperAdmin := claims != nil && claims.HasRole("super_admin")
	ctx := c.Request.Context()

	result, err := h.roleService.Update(ctx, id, &req, updatedBy, isSuperAdmin)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "Role updated successfully", result)
}

func (h *RoleHandler) UpdatePermissions(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Role ID is required", "")
		return
	}

	var req dto.UpdatePermissionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	updatedBy := middleware.MustGetUserID(c)
	claims, _ := middleware.GetUserFromContext(c)
	isSuperAdmin := claims != nil && claims.HasRole("super_admin")
	ctx := c.Request.Context()

	result, err := h.roleService.UpdatePermissions(ctx, id, &req, updatedBy, isSuperAdmin)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "Permissions updated successfully", result)
}

func (h *RoleHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Role ID is required", "")
		return
	}

	deletedBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	err := h.roleService.Delete(ctx, id, deletedBy)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "Role deleted successfully", nil)
}
