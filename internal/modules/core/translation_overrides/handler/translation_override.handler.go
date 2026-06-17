package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go/internal/middleware"
	"venturo-skeleton-go/internal/modules/core/translation_overrides/dto"
	"venturo-skeleton-go/internal/modules/core/translation_overrides/service"
	"venturo-skeleton-go/internal/shared/response"
)

type Handler struct {
	svc *service.Service
}

func NewHandler(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) List(c *gin.Context) {
	clientID := c.Param("id")
	result, err := h.svc.List(c.Request.Context(), clientID)
	if err != nil {
		response.RenderError(c, err)
		return
	}
	response.Success(c, http.StatusOK, "Translation overrides retrieved successfully", result)
}

func (h *Handler) GetByKey(c *gin.Context) {
	clientID := c.Param("id")
	key := c.Param("key")
	result, err := h.svc.GetByKey(c.Request.Context(), clientID, key)
	if err != nil {
		response.RenderError(c, err)
		return
	}
	response.Success(c, http.StatusOK, "Translation override retrieved successfully", result)
}

func (h *Handler) Create(c *gin.Context) {
	clientID := c.Param("id")

	var req dto.CreateTranslationOverrideRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	actorID, err := middleware.GetUserID(c)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "Unauthorized", "")
		return
	}

	result, err := h.svc.Create(c.Request.Context(), clientID, &req, actorID)
	if err != nil {
		response.RenderError(c, err)
		return
	}
	response.Success(c, http.StatusCreated, "Translation override created successfully", result)
}

func (h *Handler) Update(c *gin.Context) {
	clientID := c.Param("id")
	key := c.Param("key")

	var req dto.UpdateTranslationOverrideRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	actorID, err := middleware.GetUserID(c)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "Unauthorized", "")
		return
	}

	result, err := h.svc.Update(c.Request.Context(), clientID, key, &req, actorID)
	if err != nil {
		response.RenderError(c, err)
		return
	}
	response.Success(c, http.StatusOK, "Translation override updated successfully", result)
}

func (h *Handler) Delete(c *gin.Context) {
	clientID := c.Param("id")
	key := c.Param("key")
	if err := h.svc.Delete(c.Request.Context(), clientID, key); err != nil {
		response.RenderError(c, err)
		return
	}
	response.Success(c, http.StatusOK, "Translation override deleted successfully", nil)
}

func (h *Handler) PublicBySlug(c *gin.Context) {
	slug := c.Query("slug")
	if slug == "" {
		response.Error(c, http.StatusBadRequest, "slug query parameter is required", "")
		return
	}

	result, err := h.svc.PublicMapBySlug(c.Request.Context(), slug)
	if err != nil {
		response.RenderError(c, err)
		return
	}
	response.Success(c, http.StatusOK, "Translations retrieved successfully", result)
}
