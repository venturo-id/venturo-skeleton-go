package api_key

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go-simple/internal/middleware"
	"venturo-skeleton-go-simple/internal/modules/core/api_key/handler"
	"venturo-skeleton-go-simple/internal/modules/core/api_key/repository"
	"venturo-skeleton-go-simple/internal/modules/core/api_key/service"
)

// RoleRepository interface for role operations
type RoleRepository interface {
	GetUserPermissions(ctx context.Context, userID string) ([]string, error)
	GetUserRoleNames(ctx context.Context, userID string) ([]string, error)
}

// ApiKeyModule holds all components of the API key module
type ApiKeyModule struct {
	Handler    *handler.ApiKeyHandler
	Service    *service.ApiKeyService
	Repository *repository.ApiKeyRepository
}

// Initialize creates and wires all dependencies for the API key module
func Initialize(db *pgxpool.Pool, roleRepo RoleRepository) *ApiKeyModule {
	repo := repository.NewApiKeyRepository(db)
	svc := service.NewApiKeyService(repo, roleRepo)
	hdlr := handler.NewApiKeyHandler(svc)

	return &ApiKeyModule{
		Handler:    hdlr,
		Service:    svc,
		Repository: repo,
	}
}

// SetupRoutes registers all routes for the API key module
func (m *ApiKeyModule) SetupRoutes(router *gin.RouterGroup) {
	// All routes require JWT auth; keys are scoped to the calling user
	apiKeys := router.Group("/api-keys")
	apiKeys.Use(middleware.JWTAuth())
	{
		// Create a new API key
		apiKeys.POST("", m.Handler.Create)

		// List all API keys for the user
		apiKeys.GET("", m.Handler.List)

		// Get a specific API key
		apiKeys.GET("/:id", m.Handler.GetByID)

		// Update an API key
		apiKeys.PATCH("/:id", m.Handler.Update)

		// Revoke an API key
		apiKeys.DELETE("/:id", m.Handler.Revoke)
	}
}
