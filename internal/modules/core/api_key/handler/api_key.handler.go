package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go-simple/internal/middleware"
	"venturo-skeleton-go-simple/internal/modules/core/api_key/dto"
	"venturo-skeleton-go-simple/internal/modules/core/api_key/service"
	"venturo-skeleton-go-simple/internal/shared/response"
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
		response.Error(c, http.StatusBadRequest, "Invalid request payload", err.Error())
		return
	}

	userID, err := middleware.GetUserID(c)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "Unauthorized", err.Error())
		return
	}

	result, err := h.apiKeyService.Create(c.Request.Context(), &req, userID, userID)
	if err != nil {
		h.handleError(c, err)
		return
	}

	response.Success(c, http.StatusCreated, "API key created successfully", result)
}

func (h *ApiKeyHandler) List(c *gin.Context) {
	var params dto.ApiKeyQueryParams
	if err := c.ShouldBindQuery(&params); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid query parameters", err.Error())
		return
	}

	userID, err := middleware.GetUserID(c)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "Unauthorized", err.Error())
		return
	}

	result, err := h.apiKeyService.List(c.Request.Context(), userID, &params)
	if err != nil {
		h.handleError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "API keys retrieved successfully", result)
}

func (h *ApiKeyHandler) GetByID(c *gin.Context) {
	id := c.Param("id")

	userID, err := middleware.GetUserID(c)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "Unauthorized", err.Error())
		return
	}

	result, err := h.apiKeyService.GetByID(c.Request.Context(), id, userID)
	if err != nil {
		h.handleError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "API key retrieved successfully", result)
}

func (h *ApiKeyHandler) Update(c *gin.Context) {
	id := c.Param("id")

	var req dto.UpdateApiKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", err.Error())
		return
	}

	userID, err := middleware.GetUserID(c)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "Unauthorized", err.Error())
		return
	}

	result, err := h.apiKeyService.Update(c.Request.Context(), id, userID, &req)
	if err != nil {
		h.handleError(c, err)
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
		response.Error(c, http.StatusUnauthorized, "Unauthorized", err.Error())
		return
	}

	if err := h.apiKeyService.Revoke(c.Request.Context(), id, userID, &req); err != nil {
		h.handleError(c, err)
		return
	}

	response.Success(c, http.StatusOK, "API key revoked successfully", nil)
}

// handleError handles service errors and returns appropriate HTTP responses
func (h *ApiKeyHandler) handleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrApiKeyNotFound):
		response.Error(c, http.StatusNotFound, "API key not found", "")
	case errors.Is(err, service.ErrApiKeyRevoked):
		response.Error(c, http.StatusUnauthorized, "API key has been revoked", "")
	case errors.Is(err, service.ErrApiKeyExpired):
		response.Error(c, http.StatusUnauthorized, "API key has expired", "")
	case errors.Is(err, service.ErrApiKeyIPNotAllowed):
		response.Error(c, http.StatusForbidden, "IP address not allowed for this API key", "")
	case errors.Is(err, service.ErrApiKeyInvalidSecret):
		response.Error(c, http.StatusUnauthorized, "Invalid API key", "")
	case errors.Is(err, service.ErrApiKeyInvalidFormat):
		response.Error(c, http.StatusBadRequest, "Invalid API key format", "")
	case errors.Is(err, service.ErrApiKeyInvalidScope):
		response.Error(c, http.StatusBadRequest, "Invalid permission scope", err.Error())
	case errors.Is(err, service.ErrApiKeyUserInactive):
		response.Error(c, http.StatusUnauthorized, "API key owner account is inactive", "")
	default:
		response.Error(c, http.StatusInternalServerError, "Internal server error", err.Error())
	}
}
