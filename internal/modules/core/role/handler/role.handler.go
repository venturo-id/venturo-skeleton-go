package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go-simple/internal/middleware"
	"venturo-skeleton-go-simple/internal/modules/core/role/dto"
	"venturo-skeleton-go-simple/internal/modules/core/role/service"
	"venturo-skeleton-go-simple/internal/shared/response"
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
		response.Error(c, http.StatusBadRequest, "Invalid query parameters", err.Error())
		return
	}

	claims, _ := middleware.GetUserFromContext(c)
	isSuperAdmin := claims != nil && claims.HasRole("super_admin")

	// Non-super admin: hide "super_admin" so it cannot be picked up and
	// assigned from the role list.
	if !isSuperAdmin {
		params.ExcludeCodes = []string{"super_admin"}
	}

	ctx := c.Request.Context()
	result, err := h.roleService.GetAll(ctx, &params)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "Failed to get roles", err.Error())
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
		if errors.Is(err, service.ErrRoleNotFound) {
			response.Error(c, http.StatusNotFound, "Role not found", "")
			return
		}
		response.Error(c, http.StatusInternalServerError, "Failed to get role", err.Error())
		return
	}

	response.Success(c, http.StatusOK, "Role retrieved successfully", result)
}

func (h *RoleHandler) Create(c *gin.Context) {
	var req dto.CreateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", err.Error())
		return
	}

	createdBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	result, err := h.roleService.Create(ctx, &req, createdBy)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrCodeAlreadyExists):
			response.Error(c, http.StatusConflict, "Role code already exists", "")
		case errors.Is(err, service.ErrInvalidPermissions):
			response.Error(c, http.StatusBadRequest, "Invalid permissions format", "")
		default:
			response.Error(c, http.StatusInternalServerError, "Failed to create role", err.Error())
		}
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
		response.Error(c, http.StatusBadRequest, "Invalid request payload", err.Error())
		return
	}

	updatedBy := middleware.MustGetUserID(c)
	claims, _ := middleware.GetUserFromContext(c)
	isSuperAdmin := claims != nil && claims.HasRole("super_admin")
	ctx := c.Request.Context()

	result, err := h.roleService.Update(ctx, id, &req, updatedBy, isSuperAdmin)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrRoleNotFound):
			response.Error(c, http.StatusNotFound, "Role not found", "")
		case errors.Is(err, service.ErrCannotModifySystem):
			response.Error(c, http.StatusForbidden, "Cannot modify system role", "")
		case errors.Is(err, service.ErrInvalidPermissions):
			response.Error(c, http.StatusBadRequest, "Invalid permissions format", "")
		default:
			response.Error(c, http.StatusInternalServerError, "Failed to update role", err.Error())
		}
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
		response.Error(c, http.StatusBadRequest, "Invalid request payload", err.Error())
		return
	}

	updatedBy := middleware.MustGetUserID(c)
	claims, _ := middleware.GetUserFromContext(c)
	isSuperAdmin := claims != nil && claims.HasRole("super_admin")
	ctx := c.Request.Context()

	result, err := h.roleService.UpdatePermissions(ctx, id, &req, updatedBy, isSuperAdmin)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrRoleNotFound):
			response.Error(c, http.StatusNotFound, "Role not found", "")
		case errors.Is(err, service.ErrCannotModifySystem):
			response.Error(c, http.StatusForbidden, "Cannot modify system role", "")
		case errors.Is(err, service.ErrInvalidPermissions):
			response.Error(c, http.StatusBadRequest, "Invalid permissions format", "")
		default:
			response.Error(c, http.StatusInternalServerError, "Failed to update permissions", err.Error())
		}
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
		switch {
		case errors.Is(err, service.ErrRoleNotFound):
			response.Error(c, http.StatusNotFound, "Role not found", "")
		case errors.Is(err, service.ErrCannotDeleteSystem):
			response.Error(c, http.StatusForbidden, "Cannot delete system role", "")
		default:
			response.Error(c, http.StatusInternalServerError, "Failed to delete role", err.Error())
		}
		return
	}

	response.Success(c, http.StatusOK, "Role deleted successfully", nil)
}
