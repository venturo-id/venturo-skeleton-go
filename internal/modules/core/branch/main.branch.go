package branch

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go/internal/middleware"
	"venturo-skeleton-go/internal/modules/core/branch/handler"
	"venturo-skeleton-go/internal/modules/core/branch/repository"
	"venturo-skeleton-go/internal/modules/core/branch/service"
)

type BranchModule struct {
	Handler              *handler.BranchHandler
	Service              *service.BranchService
	Repository           *repository.BranchRepository
	UserBranchRepository *repository.UserBranchRepository
	UserBranchService    *service.UserBranchService
	UserBranchHandler    *handler.UserBranchHandler
}

// Initialize initializes the branch module with all dependencies
func Initialize(db *pgxpool.Pool) *BranchModule {
	branchRepo := repository.NewBranchRepository(db)
	branchService := service.NewBranchService(branchRepo)
	branchHandler := handler.NewBranchHandler(branchService)

	userBranchRepo := repository.NewUserBranchRepository(db)
	userBranchService := service.NewUserBranchService(userBranchRepo)
	userBranchHandler := handler.NewUserBranchHandler(userBranchService)

	return &BranchModule{
		Handler:              branchHandler,
		Service:              branchService,
		Repository:           branchRepo,
		UserBranchRepository: userBranchRepo,
		UserBranchService:    userBranchService,
		UserBranchHandler:    userBranchHandler,
	}
}

// SetupRoutes registers all branch module routes
func (m *BranchModule) SetupRoutes(router *gin.RouterGroup) {
	// Multi-company listing. Deliberately NOT behind CompanyContext because it
	// spans every company the caller is a member of. BranchScope still narrows
	// results by user_branches when applicable.
	multi := router.Group("/branches")
	multi.Use(middleware.JWTAuth(), middleware.BranchScope())
	{
		multi.GET("/by-companies", m.Handler.GetAllByCompanies)
	}

	branches := router.Group("/branches")
	branches.Use(middleware.JWTAuth(), middleware.CompanyContext(), middleware.BranchScope())
	{
		branches.GET("", m.Handler.GetAll)
		branches.GET("/:id", m.Handler.GetByID)
		branches.POST("", middleware.RequirePermission("branches:create"), m.Handler.Create)
		branches.PUT("/:id", middleware.RequirePermission("branches:update"), m.Handler.Update)
		branches.DELETE("/:id", middleware.RequirePermission("branches:delete"), m.Handler.Delete)
	}
}

// SetupUserBranchRoutes registers the user-branch assignment routes under
// /users/:id/branches. Separate from SetupRoutes so users-related endpoints
// cluster under the /users path like company memberships do.
func (m *BranchModule) SetupUserBranchRoutes(router *gin.RouterGroup) {
	users := router.Group("/users")
	users.Use(middleware.JWTAuth())
	{
		users.GET("/:id/branches", m.UserBranchHandler.GetUserBranches)
		users.PUT("/:id/branches",
			middleware.RequirePermission("company_users:update"),
			m.UserBranchHandler.SyncUserBranches,
		)
	}
}
