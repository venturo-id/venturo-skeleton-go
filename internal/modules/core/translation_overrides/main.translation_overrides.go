package translation_overrides

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go/internal/middleware"
	clientSvc "venturo-skeleton-go/internal/modules/core/client/service"
	"venturo-skeleton-go/internal/modules/core/translation_overrides/handler"
	"venturo-skeleton-go/internal/modules/core/translation_overrides/repository"
	"venturo-skeleton-go/internal/modules/core/translation_overrides/service"
)

type Module struct {
	Handler    *handler.Handler
	Service    *service.Service
	Repository *repository.Repository
}

func Initialize(db *pgxpool.Pool, clientService *clientSvc.Service) *Module {
	repo := repository.NewRepository(db)
	svc := service.NewService(repo, clientService)
	hdlr := handler.NewHandler(svc)

	return &Module{
		Handler:    hdlr,
		Service:    svc,
		Repository: repo,
	}
}

// SetupRoutes mounts:
//   - GET  /core/v1/translation-overrides?slug=xxx — public FE bootstrap
//   - /core/v1/admin/clients/:id/translation-overrides/* — CRUD scoped
//     to the client in the URL
//
// Authorization stack for admin routes:
//  1. JWTAuth            — must be logged in
//  2. RequireClientScope — JWT.client_id must match :id (super_admin
//     bypasses)
//  3. RequirePermission  — caller must hold the matching action on
//     "core.translation_overrides" resource
//     (super_admin bypasses)
//
// Both layers are applied because they answer different questions:
// scope = "which client can I touch", permission = "what actions am I
// allowed to perform". The administrator role (default for registrants)
// carries this resource at admin level — see seeders/core/001_roles.sql.
//
// Param name is :id (not :client_id) to align with the clients module's
// existing :id — Gin's tree router refuses conflicting param names at
// the same position.
func (m *Module) SetupRoutes(router *gin.RouterGroup) {
	// Public (no auth) — FE bootstrap.
	router.GET("/translation-overrides", m.Handler.PublicBySlug)

	admin := router.Group("/admin/clients/:id/translation-overrides")
	admin.Use(middleware.JWTAuth())
	admin.Use(middleware.RequireClientScope())
	{
		admin.GET("", middleware.RequirePermission("translation_overrides:read"), m.Handler.List)
		admin.GET("/:key", middleware.RequirePermission("translation_overrides:read"), m.Handler.GetByKey)
		admin.POST("", middleware.RequirePermission("translation_overrides:create"), m.Handler.Create)
		admin.PUT("/:key", middleware.RequirePermission("translation_overrides:update"), m.Handler.Update)
		admin.DELETE("/:key", middleware.RequirePermission("translation_overrides:delete"), m.Handler.Delete)
	}
}
