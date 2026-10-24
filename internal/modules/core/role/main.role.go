package role

import (
	"github.com/gin-gonic/gin"

	"timebox-be/internal/middleware"
	"timebox-be/internal/modules/core/role/handler"
	"timebox-be/internal/modules/core/role/repository"
	"timebox-be/internal/modules/core/role/service"
	"timebox-be/internal/shared/audit"
)

type RoleModule struct {
	Handler *handler.RoleHandler
	Service *service.RoleService
}

// Initialize initializes the role module with all dependencies. The
// repository is built by the router, because authz and other modules
// read roles through it too.
func Initialize(roleRepo *repository.RoleRepository, invalidator service.PermissionCacheInvalidator, recorder audit.Recorder) *RoleModule {
	roleService := service.NewRoleService(roleRepo, invalidator, recorder)

	return &RoleModule{
		Handler: handler.NewRoleHandler(roleService),
		Service: roleService,
	}
}

// roleReaders are the permissions that may list and read roles.
var roleReaders = []string{"roles:read", "users:create", "users:update"}

// SetupRoutes registers all role module routes
func (m *RoleModule) SetupRoutes(router *gin.RouterGroup) {
	roles := router.Group("/roles")
	roles.Use(middleware.JWTAuth())
	{
		// Readable by role managers and by user managers, who need the list
		// to pick a role when creating or editing a user.
		roles.GET("", middleware.RequireAnyPermission(roleReaders...), m.Handler.GetAll)
		roles.GET("/permission-catalog", middleware.RequireAnyPermission(roleReaders...), m.Handler.GetPermissionCatalog)
		roles.GET("/:id", middleware.RequireAnyPermission(roleReaders...), m.Handler.GetByID)
		roles.POST("", middleware.RequirePermission("roles:create"), m.Handler.Create)
		roles.PUT("/:id", middleware.RequirePermission("roles:update"), m.Handler.Update)
		roles.PUT("/:id/permissions", middleware.RequirePermission("roles:update"), m.Handler.UpdatePermissions)
		roles.DELETE("/:id", middleware.RequirePermission("roles:delete"), m.Handler.Delete)
	}
}
