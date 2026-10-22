package user

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go-simple/internal/middleware"
	"venturo-skeleton-go-simple/internal/modules/core/user/handler"
	"venturo-skeleton-go-simple/internal/modules/core/user/repository"
	"venturo-skeleton-go-simple/internal/modules/core/user/service"
)

type UserModule struct {
	Handler *handler.UserHandler
	Service *service.UserService
}

// Initialize initializes the user module with all dependencies
func Initialize(db *pgxpool.Pool) *UserModule {
	userRepo := repository.NewUserRepository(db)
	userService := service.NewUserService(userRepo)
	userHandler := handler.NewUserHandler(userService)

	return &UserModule{
		Handler: userHandler,
		Service: userService,
	}
}

// SetupRoutes registers all user module routes
func (m *UserModule) SetupRoutes(router *gin.RouterGroup) {
	users := router.Group("/users")
	users.Use(middleware.JWTAuth())
	{
		// Current user endpoints (must be before /:id to avoid conflict)
		users.GET("/me", m.Handler.GetMe)
		users.PUT("/me", m.Handler.UpdateMe)
		users.PUT("/me/password", m.Handler.ChangePassword)

		// User management endpoints
		users.GET("", middleware.RequirePermission("user-management:read"), m.Handler.GetAll)
		users.GET("/:id", middleware.RequirePermission("user-management:read"), m.Handler.GetByID)
		users.POST("", middleware.RequirePermission("user-management:create"), m.Handler.Create)
		users.PUT("/:id", middleware.RequirePermission("user-management:update"), m.Handler.Update)
		users.DELETE("/:id", middleware.RequirePermission("user-management:delete"), m.Handler.Delete)
	}
}
