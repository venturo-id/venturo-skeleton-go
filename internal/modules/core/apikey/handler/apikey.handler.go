package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"timebox-be/internal/middleware"
	"timebox-be/internal/modules/core/apikey/dto"
	"timebox-be/internal/modules/core/apikey/service"
	"timebox-be/internal/shared/request"
	"timebox-be/internal/shared/response"
)

type APIKeyHandler struct {
	apiKeyService *service.APIKeyService
}

func NewAPIKeyHandler(apiKeyService *service.APIKeyService) *APIKeyHandler {
	return &APIKeyHandler{apiKeyService: apiKeyService}
}

func (h *APIKeyHandler) Create(c *gin.Context) {
	var req dto.CreateAPIKeyRequest
	if !request.BindJSON(c, &req) {
		return
	}

	result, err := h.apiKeyService.Create(c.Request.Context(), &req, middleware.MustGetUserID(c))
	if err != nil {
		h.handleError(c, err, "Failed to create API key")
		return
	}

	response.Success(c, http.StatusCreated, "API key created successfully", result)
}

func (h *APIKeyHandler) List(c *gin.Context) {
	var params dto.APIKeyQueryParams
	if !request.BindQuery(c, &params) {
		return
	}

	apiKeys, total, err := h.apiKeyService.List(c.Request.Context(), middleware.MustGetUserID(c), &params)
	if err != nil {
		h.handleError(c, err, "Failed to get API keys")
		return
	}

	response.SuccessWithPagination(c, http.StatusOK, "API keys retrieved successfully",
		apiKeys, params.Page, params.Limit, total)
}

func (h *APIKeyHandler) GetByID(c *gin.Context) {
	id, ok := request.UUIDParam(c, "id")
	if !ok {
		return
	}

	result, err := h.apiKeyService.GetByID(c.Request.Context(), id, middleware.MustGetUserID(c))
	if err != nil {
		h.handleError(c, err, "Failed to get API key")
		return
	}

	response.Success(c, http.StatusOK, "API key retrieved successfully", result)
}

func (h *APIKeyHandler) Update(c *gin.Context) {
	id, ok := request.UUIDParam(c, "id")
	if !ok {
		return
	}

	var req dto.UpdateAPIKeyRequest
	if !request.BindJSON(c, &req) {
		return
	}

	result, err := h.apiKeyService.Update(c.Request.Context(), id, middleware.MustGetUserID(c), &req)
	if err != nil {
		h.handleError(c, err, "Failed to update API key")
		return
	}

	response.Success(c, http.StatusOK, "API key updated successfully", result)
}

func (h *APIKeyHandler) Revoke(c *gin.Context) {
	id, ok := request.UUIDParam(c, "id")
	if !ok {
		return
	}

	var req dto.RevokeAPIKeyRequest
	if !request.BindOptionalJSON(c, &req) {
		return
	}

	if err := h.apiKeyService.Revoke(c.Request.Context(), id, middleware.MustGetUserID(c), &req); err != nil {
		h.handleError(c, err, "Failed to revoke API key")
		return
	}

	response.Success(c, http.StatusOK, "API key revoked successfully", nil)
}

// handleError maps service errors to responses. Anything unexpected is a
// 500 carrying only fallback.
func (h *APIKeyHandler) handleError(c *gin.Context, err error, fallback string) {
	switch {
	case errors.Is(err, service.ErrAPIKeyNotFound):
		response.Error(c, http.StatusNotFound, "API key not found", "")
	case errors.Is(err, service.ErrAPIKeyAlreadyRevoked):
		response.Error(c, http.StatusConflict, "API key is already revoked", "")
	case errors.Is(err, service.ErrAPIKeyInvalidScope), errors.Is(err, service.ErrInvalidExpiry):
		// The message is built by the service from the request alone.
		response.Error(c, http.StatusBadRequest, "Invalid API key settings", err.Error())
	default:
		response.InternalError(c, fallback, err)
	}
}
