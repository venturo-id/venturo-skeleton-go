package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"timebox-be/internal/middleware"
	"timebox-be/internal/modules/core/role/dto"
	"timebox-be/internal/modules/core/role/service"
	"timebox-be/internal/shared/request"
	"timebox-be/internal/shared/response"
	"timebox-be/pkg/jwt"
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
	if !request.BindQuery(c, &params) {
		return
	}

	// Non-super admin: hide "super_admin" so it cannot be picked up and
	// assigned from the role list.
	if !middleware.IsSuperAdmin(c) {
		params.ExcludeCodes = []string{jwt.RoleSuperAdmin}
	}

	roles, total, err := h.roleService.List(c.Request.Context(), &params)
	if err != nil {
		h.handleError(c, err, "Failed to get roles")
		return
	}

	response.SuccessWithPagination(c, http.StatusOK, "Roles retrieved successfully",
		roles, params.Page, params.Limit, total)
}

// GetPermissionCatalog lists the resources and levels a role can grant,
// for role editors.
func (h *RoleHandler) GetPermissionCatalog(c *gin.Context) {
	response.Success(c, http.StatusOK, "Permission catalog retrieved successfully", h.roleService.PermissionCatalog())
}

func (h *RoleHandler) GetByID(c *gin.Context) {
	id, ok := request.UUIDParam(c, "id")
	if !ok {
		return
	}

	result, err := h.roleService.GetByID(c.Request.Context(), id)
	if err != nil {
		h.handleError(c, err, "Failed to get role")
		return
	}

	response.Success(c, http.StatusOK, "Role retrieved successfully", result)
}

func (h *RoleHandler) Create(c *gin.Context) {
	var req dto.CreateRoleRequest
	if !request.BindJSON(c, &req) {
		return
	}

	result, err := h.roleService.Create(c.Request.Context(), &req, middleware.MustGetUserID(c), middleware.Caller(c))
	if err != nil {
		h.handleError(c, err, "Failed to create role")
		return
	}

	response.Success(c, http.StatusCreated, "Role created successfully", result)
}

func (h *RoleHandler) Update(c *gin.Context) {
	id, ok := request.UUIDParam(c, "id")
	if !ok {
		return
	}

	var req dto.UpdateRoleRequest
	if !request.BindJSON(c, &req) {
		return
	}

	result, err := h.roleService.Update(c.Request.Context(), id, &req, middleware.MustGetUserID(c), middleware.Caller(c))
	if err != nil {
		h.handleError(c, err, "Failed to update role")
		return
	}

	response.Success(c, http.StatusOK, "Role updated successfully", result)
}

func (h *RoleHandler) UpdatePermissions(c *gin.Context) {
	id, ok := request.UUIDParam(c, "id")
	if !ok {
		return
	}

	var req dto.UpdatePermissionsRequest
	if !request.BindJSON(c, &req) {
		return
	}

	result, err := h.roleService.UpdatePermissions(c.Request.Context(), id, &req, middleware.MustGetUserID(c), middleware.Caller(c))
	if err != nil {
		h.handleError(c, err, "Failed to update permissions")
		return
	}

	response.Success(c, http.StatusOK, "Permissions updated successfully", result)
}

func (h *RoleHandler) Delete(c *gin.Context) {
	id, ok := request.UUIDParam(c, "id")
	if !ok {
		return
	}

	if err := h.roleService.Delete(c.Request.Context(), id, middleware.MustGetUserID(c), middleware.Caller(c)); err != nil {
		h.handleError(c, err, "Failed to delete role")
		return
	}

	response.Success(c, http.StatusOK, "Role deleted successfully", nil)
}

// handleError maps service errors to responses. Anything unexpected is a
// 500 carrying only fallback.
func (h *RoleHandler) handleError(c *gin.Context, err error, fallback string) {
	switch {
	case errors.Is(err, service.ErrRoleNotFound):
		response.Error(c, http.StatusNotFound, "Role not found", "")
	case errors.Is(err, service.ErrCodeAlreadyExists):
		response.Error(c, http.StatusConflict, "Role code already exists", "")
	case errors.Is(err, service.ErrInvalidPermissions):
		// The message is built by the service from the request alone.
		response.Error(c, http.StatusBadRequest, "Invalid permissions", err.Error())
	case errors.Is(err, service.ErrCannotModifySystem):
		response.Error(c, http.StatusForbidden, "Cannot modify system role", "")
	case errors.Is(err, service.ErrCannotDeleteSystem):
		response.Error(c, http.StatusForbidden, "Cannot delete system role", "")
	case errors.Is(err, service.ErrSuperAdminRoleLocked):
		response.Error(c, http.StatusForbidden, "The super_admin role cannot be deactivated or given permissions", "")
	case errors.Is(err, service.ErrExceedsCaller):
		response.Error(c, http.StatusForbidden, "Role grants permissions you do not hold", "")
	default:
		response.InternalError(c, fallback, err)
	}
}
