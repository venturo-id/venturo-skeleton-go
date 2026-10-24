package user

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"timebox-be/internal/middleware"
	"timebox-be/internal/modules/core/user/handler"
	"timebox-be/internal/modules/core/user/repository"
	"timebox-be/internal/modules/core/user/service"
	"timebox-be/internal/shared/audit"
	"timebox-be/pkg/avatar"
)

type UserModule struct {
	Handler *handler.UserHandler
	Service *service.UserService
}

// Initialize initializes the user module with all dependencies. The
// repository is built by the router, because the auth module uses it too.
// avatars is nil when R2 is not configured: the avatar endpoints then
// answer 503.
func Initialize(
	db *pgxpool.Pool,
	userRepo *repository.UserRepository,
	roles service.RoleLookup,
	invalidator service.PermissionCacheInvalidator,
	credentials service.Credentials,
	recorder audit.Recorder,
	avatars *avatar.Store,
) *UserModule {
	userService := service.NewUserService(db, userRepo, roles, invalidator, credentials, recorder, avatars)

	return &UserModule{
		Handler: handler.NewUserHandler(userService),
		Service: userService,
	}
}

// SetupRoutes registers all user module routes
func (m *UserModule) SetupRoutes(router *gin.RouterGroup) {
	users := router.Group("/users")
	users.Use(middleware.JWTAuth())
	{
		// Current user endpoints (must be before /:id to avoid conflict).
		// Reading the profile works with an API key; changing it does not.
		users.GET("/me", m.Handler.GetMe)
		users.PUT("/me", middleware.DenyAPIKeyAuth(), m.Handler.UpdateMe)
		users.PUT("/me/password", middleware.DenyAPIKeyAuth(), m.Handler.ChangePassword)
		users.PUT("/me/avatar", middleware.DenyAPIKeyAuth(), m.Handler.UploadAvatar)
		users.DELETE("/me/avatar", middleware.DenyAPIKeyAuth(), m.Handler.RemoveAvatar)

		// User management endpoints
		users.GET("", middleware.RequirePermission("users:read"), m.Handler.GetAll)
		users.GET("/:id", middleware.RequirePermission("users:read"), m.Handler.GetByID)
		users.POST("", middleware.RequirePermission("users:create"), m.Handler.Create)
		users.PUT("/:id", middleware.RequirePermission("users:update"), m.Handler.Update)
		users.DELETE("/:id", middleware.RequirePermission("users:delete"), m.Handler.Delete)
	}
}
