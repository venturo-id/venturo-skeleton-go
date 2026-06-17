package company

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go/internal/middleware"
	"venturo-skeleton-go/internal/modules/core/company/handler"
	"venturo-skeleton-go/internal/modules/core/company/repository"
	"venturo-skeleton-go/internal/modules/core/company/service"
)

type CompanyModule struct {
	Handler        *handler.CompanyHandler
	Service        *service.CompanyService
	Repository     *repository.CompanyRepository
	UserRepository *repository.CompanyUserRepository
}

// Initialize initializes the company module with all dependencies
func Initialize(db *pgxpool.Pool) *CompanyModule {
	companyRepo := repository.NewCompanyRepository(db)
	companyUserRepo := repository.NewCompanyUserRepository(db)
	companyService := service.NewCompanyService(companyRepo, companyUserRepo)
	companyHandler := handler.NewCompanyHandler(companyService)

	return &CompanyModule{
		Handler:        companyHandler,
		Service:        companyService,
		Repository:     companyRepo,
		UserRepository: companyUserRepo,
	}
}

// SetupRoutes registers all company module routes
func (m *CompanyModule) SetupRoutes(router *gin.RouterGroup) {
	companies := router.Group("/companies")
	companies.Use(middleware.JWTAuth())
	{
		// Company CRUD
		companies.GET("", m.Handler.GetAll)
		companies.GET("/trash", middleware.RequirePermission("companies:delete"), m.Handler.GetTrash)
		companies.GET("/:id", m.Handler.GetByID)
		companies.POST("", m.Handler.Create)
		companies.PUT("/:id", middleware.RequirePermission("companies:update"), m.Handler.Update)
		companies.DELETE("/:id", middleware.RequirePermission("companies:delete"), m.Handler.Delete)
		companies.PATCH("/:id/restore", middleware.RequirePermission("companies:delete"), m.Handler.Restore)

		// Hierarchy endpoints
		companies.GET("/:id/children", m.Handler.GetChildren)
		companies.GET("/:id/ancestors", m.Handler.GetAncestors)

		// Company user management
		companies.GET("/:id/users", m.Handler.GetUsers)
		companies.POST("/:id/users", middleware.RequirePermission("company_users:create"), m.Handler.AddUser)
		companies.PUT("/:id/users/:user_id", middleware.RequirePermission("company_users:update"), m.Handler.UpdateUser)
		companies.DELETE("/:id/users/:user_id", middleware.RequirePermission("company_users:delete"), m.Handler.RemoveUser)
	}
}

// SetupUserCompanyRoutes registers user-company management routes under /users
func (m *CompanyModule) SetupUserCompanyRoutes(router *gin.RouterGroup) {
	users := router.Group("/users")
	users.Use(middleware.JWTAuth())
	{
		users.GET("/:id/companies", m.Handler.GetUserCompanies)
		users.PUT("/:id/companies", middleware.RequirePermission("company_users:update"), m.Handler.SyncUserCompanies)
	}
}
