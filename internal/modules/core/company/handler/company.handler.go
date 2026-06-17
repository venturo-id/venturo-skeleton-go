package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go/internal/middleware"
	"venturo-skeleton-go/internal/modules/core/company/dto"
	"venturo-skeleton-go/internal/modules/core/company/service"
	"venturo-skeleton-go/internal/shared/response"
)

type CompanyHandler struct {
	companyService *service.CompanyService
}

func NewCompanyHandler(companyService *service.CompanyService) *CompanyHandler {
	return &CompanyHandler{
		companyService: companyService,
	}
}

func (h *CompanyHandler) GetAll(c *gin.Context) {
	var params dto.CompanyQueryParams
	if err := c.ShouldBindQuery(&params); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid query parameters", "")
		return
	}

	claims, _ := middleware.GetUserFromContext(c)
	isSuperAdmin := claims != nil && claims.HasRole("super_admin")
	var userID string
	if claims != nil {
		userID = claims.UserID
	}

	ctx := c.Request.Context()
	result, err := h.companyService.GetAll(ctx, &params, userID, isSuperAdmin)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.SuccessWithPagination(c, http.StatusOK, "Companies retrieved successfully",
		result.Companies, result.Page, result.Limit, result.Total)
}

func (h *CompanyHandler) GetByID(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Company ID is required", "")
		return
	}

	ctx := c.Request.Context()
	result, err := h.companyService.GetByID(ctx, id)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "Company retrieved successfully", result)
}

func (h *CompanyHandler) Create(c *gin.Context) {
	var req dto.CreateCompanyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	createdBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	// Check if caller is super_admin (needed for assigning different owner)
	claims, _ := middleware.GetUserFromContext(c)
	isSuperAdmin := claims != nil && claims.HasRole("super_admin")

	result, err := h.companyService.Create(ctx, &req, createdBy, isSuperAdmin)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusCreated, "Company created successfully", result)
}

func (h *CompanyHandler) Update(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Company ID is required", "")
		return
	}

	var req dto.UpdateCompanyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	updatedBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	// Check if caller is super_admin (needed for owner transfer)
	claims, _ := middleware.GetUserFromContext(c)
	isSuperAdmin := claims != nil && claims.HasRole("super_admin")

	result, err := h.companyService.Update(ctx, id, &req, updatedBy, isSuperAdmin)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "Company updated successfully", result)
}

func (h *CompanyHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Company ID is required", "")
		return
	}

	deletedBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	err := h.companyService.Delete(ctx, id, deletedBy)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "Company deleted successfully", nil)
}

func (h *CompanyHandler) GetTrash(c *gin.Context) {
	var params dto.CompanyTrashQueryParams
	if err := c.ShouldBindQuery(&params); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid query parameters", "")
		return
	}

	ctx := c.Request.Context()
	result, err := h.companyService.GetTrash(ctx, &params)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.SuccessWithPagination(c, http.StatusOK, "Deleted companies retrieved successfully",
		result.Companies, result.Page, result.Limit, result.Total)
}

func (h *CompanyHandler) Restore(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Company ID is required", "")
		return
	}

	restoredBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	result, err := h.companyService.Restore(ctx, id, restoredBy)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "Company restored successfully", result)
}

func (h *CompanyHandler) GetChildren(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Company ID is required", "")
		return
	}

	ctx := c.Request.Context()
	result, err := h.companyService.GetChildren(ctx, id)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "Children retrieved successfully", result)
}

func (h *CompanyHandler) GetAncestors(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Company ID is required", "")
		return
	}

	ctx := c.Request.Context()
	result, err := h.companyService.GetAncestors(ctx, id)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "Ancestors retrieved successfully", result)
}

func (h *CompanyHandler) GetUsers(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Company ID is required", "")
		return
	}

	var params dto.CompanyUserQueryParams
	if err := c.ShouldBindQuery(&params); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid query parameters", "")
		return
	}

	ctx := c.Request.Context()
	result, err := h.companyService.GetUsers(ctx, id, &params)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.SuccessWithPagination(c, http.StatusOK, "Users retrieved successfully",
		result.Users, result.Page, result.Limit, result.Total)
}

func (h *CompanyHandler) AddUser(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		response.Error(c, http.StatusBadRequest, "Company ID is required", "")
		return
	}

	var req dto.AddCompanyUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	invitedBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	result, err := h.companyService.AddUser(ctx, id, &req, invitedBy)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusCreated, "User added successfully", result)
}

func (h *CompanyHandler) UpdateUser(c *gin.Context) {
	id := c.Param("id")
	userID := c.Param("user_id")
	if id == "" || userID == "" {
		response.Error(c, http.StatusBadRequest, "Company ID and User ID are required", "")
		return
	}

	var req dto.UpdateCompanyUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	updatedBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	result, err := h.companyService.UpdateUser(ctx, id, userID, &req, updatedBy)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "User updated successfully", result)
}

func (h *CompanyHandler) RemoveUser(c *gin.Context) {
	id := c.Param("id")
	userID := c.Param("user_id")
	if id == "" || userID == "" {
		response.Error(c, http.StatusBadRequest, "Company ID and User ID are required", "")
		return
	}

	deletedBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	err := h.companyService.RemoveUser(ctx, id, userID, deletedBy)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "User removed successfully", nil)
}

func (h *CompanyHandler) SyncUserCompanies(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		response.Error(c, http.StatusBadRequest, "User ID is required", "")
		return
	}

	var req dto.SyncUserCompaniesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	updatedBy := middleware.MustGetUserID(c)
	ctx := c.Request.Context()

	err := h.companyService.SyncUserCompanies(ctx, userID, &req, updatedBy)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "User companies synced successfully", nil)
}

func (h *CompanyHandler) GetUserCompanies(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		response.Error(c, http.StatusBadRequest, "User ID is required", "")
		return
	}

	ctx := c.Request.Context()
	companyIDs, err := h.companyService.GetUserCompanyIDs(ctx, userID)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "User companies retrieved successfully", companyIDs)
}
