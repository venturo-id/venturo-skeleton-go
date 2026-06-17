package audit

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go/internal/middleware"
	"venturo-skeleton-go/internal/shared/response"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) List(c *gin.Context) {
	var params ListQueryParams
	if err := c.ShouldBindQuery(&params); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid query parameters", "")
		return
	}
	if params.Page == 0 {
		params.Page = 1
	}
	if params.Limit == 0 {
		params.Limit = 50
	}

	// Cross-filter rule: reff_id only makes sense scoped by a reff_type.
	if params.ReffID != "" && params.ReffType == "" {
		response.Error(c, http.StatusBadRequest, "reff_type is required when reff_id is provided", "")
		return
	}

	companyID := middleware.GetCompanyID(c)
	items, total, err := h.svc.List(c.Request.Context(), ListFilter{
		CompanyID: companyID,
		ReffType:  params.ReffType,
		ReffID:    params.ReffID,
		ActorID:   params.ActorID,
		Action:    params.Action,
		DateFrom:  params.DateFrom,
		DateTo:    params.DateTo,
		Page:      params.Page,
		Limit:     params.Limit,
	})
	if err != nil {
		response.RenderError(c, err)
		return
	}
	response.SuccessWithPagination(c, http.StatusOK, "Audit logs retrieved successfully",
		items, params.Page, params.Limit, total)
}
