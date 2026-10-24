package apikey

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"timebox-be/internal/middleware"
	"timebox-be/internal/modules/core/apikey/handler"
	"timebox-be/internal/modules/core/apikey/repository"
	"timebox-be/internal/modules/core/apikey/service"
	"timebox-be/internal/shared/audit"
)

// APIKeyModule holds all components of the API key module
type APIKeyModule struct {
	Handler *handler.APIKeyHandler
	Service *service.APIKeyService
}

// Initialize creates and wires all dependencies for the API key module
func Initialize(db *pgxpool.Pool, subjects service.SubjectReader, limiter service.RateLimiter, recorder audit.Recorder) *APIKeyModule {
	svc := service.NewAPIKeyService(repository.NewAPIKeyRepository(db), subjects, limiter, recorder)

	return &APIKeyModule{
		Handler: handler.NewAPIKeyHandler(svc),
		Service: svc,
	}
}

// SetupRoutes registers all routes for the API key module
func (m *APIKeyModule) SetupRoutes(router *gin.RouterGroup) {
	// All routes require a user session — not an API key, or a narrowly
	// scoped key could mint a broader one. Keys are scoped to the calling user.
	apiKeys := router.Group("/api-keys")
	apiKeys.Use(middleware.JWTAuth(), middleware.DenyAPIKeyAuth())
	{
		apiKeys.POST("", m.Handler.Create)
		apiKeys.GET("", m.Handler.List)
		apiKeys.GET("/:id", m.Handler.GetByID)
		apiKeys.PATCH("/:id", m.Handler.Update)
		apiKeys.DELETE("/:id", m.Handler.Revoke)
	}
}
