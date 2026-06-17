package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go/internal/middleware"
	"venturo-skeleton-go/internal/modules/core/api_key/dto"
	"venturo-skeleton-go/internal/modules/core/api_key/service"
	"venturo-skeleton-go/internal/shared/response"
)

type ApiKeyHandler struct {
	apiKeyService *service.ApiKeyService
}

func NewApiKeyHandler(apiKeyService *service.ApiKeyService) *ApiKeyHandler {
	return &ApiKeyHandler{apiKeyService: apiKeyService}
}

func (h *ApiKeyHandler) Create(c *gin.Context) {
	var req dto.CreateApiKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	userID, err := middleware.GetUserID(c)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "Unauthorized", "")
		return
	}

	companyID := middleware.GetCompanyID(c)
	if companyID == "" {
		response.Error(c, http.StatusForbidden, "Company context required", "Please switch to a company first")
		return
	}

	result, err := h.apiKeyService.Create(c.Request.Context(), &req, userID, companyID, userID)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusCreated, "API key created successfully", result)
}

func (h *ApiKeyHandler) List(c *gin.Context) {
	var params dto.ApiKeyQueryParams
	if err := c.ShouldBindQuery(&params); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid query parameters", "")
		return
	}

	userID, err := middleware.GetUserID(c)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "Unauthorized", "")
		return
	}

	companyID := middleware.GetCompanyID(c)
	if companyID == "" {
		response.Error(c, http.StatusForbidden, "Company context required", "Please switch to a company first")
		return
	}

	result, err := h.apiKeyService.List(c.Request.Context(), userID, companyID, &params)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "API keys retrieved successfully", result)
}

func (h *ApiKeyHandler) GetByID(c *gin.Context) {
	id := c.Param("id")

	companyID := middleware.GetCompanyID(c)
	if companyID == "" {
		response.Error(c, http.StatusForbidden, "Company context required", "Please switch to a company first")
		return
	}

	result, err := h.apiKeyService.GetByID(c.Request.Context(), id, companyID)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "API key retrieved successfully", result)
}

func (h *ApiKeyHandler) Update(c *gin.Context) {
	id := c.Param("id")

	var req dto.UpdateApiKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	userID, err := middleware.GetUserID(c)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "Unauthorized", "")
		return
	}

	companyID := middleware.GetCompanyID(c)
	if companyID == "" {
		response.Error(c, http.StatusForbidden, "Company context required", "Please switch to a company first")
		return
	}

	result, err := h.apiKeyService.Update(c.Request.Context(), id, companyID, userID, &req)
	if err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "API key updated successfully", result)
}

func (h *ApiKeyHandler) Revoke(c *gin.Context) {
	id := c.Param("id")

	var req dto.RevokeApiKeyRequest
	// Bind JSON if present, but don't fail if empty body
	_ = c.ShouldBindJSON(&req)

	userID, err := middleware.GetUserID(c)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "Unauthorized", "")
		return
	}

	companyID := middleware.GetCompanyID(c)
	if companyID == "" {
		response.Error(c, http.StatusForbidden, "Company context required", "Please switch to a company first")
		return
	}

	if err := h.apiKeyService.Revoke(c.Request.Context(), id, companyID, userID, &req); err != nil {
		response.RenderError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "API key revoked successfully", nil)
}
