package role

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go-simple/internal/middleware"
	"venturo-skeleton-go-simple/internal/modules/core/role/handler"
	"venturo-skeleton-go-simple/internal/modules/core/role/repository"
	"venturo-skeleton-go-simple/internal/modules/core/role/service"
)

type RoleModule struct {
	Handler    *handler.RoleHandler
	Repository *repository.RoleRepository
	Service    *service.RoleService
}

// Initialize initializes the role module with all dependencies
func Initialize(db *pgxpool.Pool) *RoleModule {
	roleRepo := repository.NewRoleRepository(db)
	roleService := service.NewRoleService(roleRepo)
	roleHandler := handler.NewRoleHandler(roleService)

	return &RoleModule{
		Handler:    roleHandler,
		Repository: roleRepo,
		Service:    roleService,
	}
}

// SetupRoutes registers all role module routes
func (m *RoleModule) SetupRoutes(router *gin.RouterGroup) {
	roles := router.Group("/roles")
	roles.Use(middleware.JWTAuth())
	{
		roles.GET("", m.Handler.GetAll)
		roles.GET("/:id", m.Handler.GetByID)
		roles.POST("", middleware.RequirePermission("roles:create"), m.Handler.Create)
		roles.PUT("/:id", middleware.RequirePermission("roles:update"), m.Handler.Update)
		roles.PUT("/:id/permissions", middleware.RequirePermission("roles:update"), m.Handler.UpdatePermissions)
		roles.DELETE("/:id", middleware.RequirePermission("roles:delete"), m.Handler.Delete)
	}
}
