package auth

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"venturo-skeleton-go-simple/internal/config"
	"venturo-skeleton-go-simple/internal/middleware"
	"venturo-skeleton-go-simple/internal/modules/core/auth/handler"
	"venturo-skeleton-go-simple/internal/modules/core/auth/repository"
	"venturo-skeleton-go-simple/internal/modules/core/auth/service"
	userRepo "venturo-skeleton-go-simple/internal/modules/core/user/repository"
)

type AuthModule struct {
	Handler *handler.AuthHandler
	Service *service.AuthService
}

// Initialize initializes the auth module with all dependencies
func Initialize(db *pgxpool.Pool, cfg *config.Config) *AuthModule {
	userRepository := userRepo.NewUserRepository(db)
	refreshTokenRepo := repository.NewRefreshTokenRepository(db)
	authService := service.NewAuthService(userRepository, refreshTokenRepo, cfg)
	authHandler := handler.NewAuthHandler(authService)

	return &AuthModule{
		Handler: authHandler,
		Service: authService,
	}
}

// SetupRoutes registers all auth module routes
func (m *AuthModule) SetupRoutes(router *gin.RouterGroup) {
	auth := router.Group("/auth")
	{
		// Public routes
		auth.POST("/signup", m.Handler.SignUp)
		auth.POST("/signin", m.Handler.SignIn)
		auth.POST("/google", m.Handler.SignInWithGoogle)
		auth.POST("/refresh", m.Handler.Refresh)

		// Protected routes
		auth.POST("/logout", middleware.JWTAuth(), m.Handler.Logout)
		auth.POST("/logout-all", middleware.JWTAuth(), m.Handler.LogoutAll)
		auth.GET("/me", middleware.JWTAuth(), m.Handler.GetMe)
	}
}
