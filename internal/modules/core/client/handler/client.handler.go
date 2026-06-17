package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go/internal/middleware"
	"venturo-skeleton-go/internal/modules/core/client/dto"
	"venturo-skeleton-go/internal/modules/core/client/service"
	"venturo-skeleton-go/internal/shared/response"
)

type Handler struct {
	svc *service.Service
}

func NewHandler(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) List(c *gin.Context) {
	result, err := h.svc.List(c.Request.Context())
	if err != nil {
		response.RenderError(c, err)
		return
	}
	response.Success(c, http.StatusOK, "Clients retrieved successfully", result)
}

func (h *Handler) GetByID(c *gin.Context) {
	id := c.Param("id")
	client, err := h.svc.GetByID(c.Request.Context(), id)
	if err != nil {
		response.RenderError(c, err)
		return
	}
	resp := service.ResponseFromDomain(client)
	response.Success(c, http.StatusOK, "Client retrieved successfully", resp)
}

func (h *Handler) Update(c *gin.Context) {
	id := c.Param("id")

	var req dto.UpdateClientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", "")
		return
	}

	actorID, err := middleware.GetUserID(c)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "Unauthorized", "")
		return
	}

	result, err := h.svc.Update(c.Request.Context(), id, &req, actorID)
	if err != nil {
		response.RenderError(c, err)
		return
	}
	response.Success(c, http.StatusOK, "Client updated successfully", result)
}
