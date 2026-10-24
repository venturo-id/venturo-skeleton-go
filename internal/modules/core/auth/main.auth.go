package auth

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"timebox-be/internal/config"
	"timebox-be/internal/middleware"
	"timebox-be/internal/modules/core/auth/handler"
	"timebox-be/internal/modules/core/auth/repository"
	"timebox-be/internal/modules/core/auth/service"
	userRepo "timebox-be/internal/modules/core/user/repository"
	"timebox-be/pkg/email"
	"timebox-be/pkg/logger"
)

type AuthModule struct {
	Handler   *handler.AuthHandler
	Service   *service.AuthService
	rateLimit config.RateLimitConfig
}

// Dependencies are what the auth module needs from the rest of the
// application. Every field is required except Google (nil disables Google
// sign-in) and Avatars (nil: Google pictures are not copied).
type Dependencies struct {
	Users    *userRepo.UserRepository
	Roles    service.RoleRepository
	Subjects service.SubjectCache
	APIKeys  service.APIKeys
	Limiter  service.RateLimiter
	Audit    service.AuditLog
	Mailer   email.EmailService
	Google   service.GoogleOAuth
	Avatars  service.Avatars
}

// Initialize initializes the auth module with all dependencies.
func Initialize(db *pgxpool.Pool, cfg *config.Config, deps Dependencies) *AuthModule {
	authService := service.NewAuthService(cfg.Security, service.Deps{
		DB:            db,
		Users:         deps.Users,
		Identities:    userRepo.NewUserIdentityRepository(db),
		RefreshTokens: repository.NewRefreshTokenRepository(db),
		AccountTokens: repository.NewAccountTokenRepository(db),
		Roles:         deps.Roles,
		Subjects:      deps.Subjects,
		APIKeys:       deps.APIKeys,
		Limiter:       deps.Limiter,
		Audit:         deps.Audit,
		Mailer:        deps.Mailer,
		Google:        deps.Google,
		Avatars:       deps.Avatars,
	})

	cookie := handler.CookieConfig{
		// Plain http is only for local development.
		Secure:         cfg.Server.Env != "development",
		MaxAge:         cfg.Security.RefreshTokenExpiration,
		AllowedOrigins: cfg.Server.CORSAllowedOrigins,
	}

	return &AuthModule{
		Handler:   handler.NewAuthHandler(authService, cookie),
		Service:   authService,
		rateLimit: cfg.RateLimit,
	}
}

// SetupRoutes registers all auth module routes
func (m *AuthModule) SetupRoutes(router *gin.RouterGroup) {
	auth := router.Group("/auth")
	limit, window := m.rateLimit.AuthLimit, m.rateLimit.AuthWindow
	{
		// Public routes — rate limited per client IP, each in its own bucket
		auth.POST("/signup", middleware.RateLimitByIP("auth:signup", limit, window), m.Handler.SignUp)
		auth.POST("/signin", middleware.RateLimitByIP("auth:signin", limit, window), m.Handler.SignIn)
		auth.GET("/google/client", middleware.RateLimitByIP("auth:google-client", limit, window), m.Handler.GoogleClient)
		auth.POST("/google", middleware.RateLimitByIP("auth:google", limit, window), m.Handler.SignInWithGoogle)
		auth.POST("/refresh", middleware.RateLimitByIP("auth:refresh", limit, window), m.Handler.Refresh)
		auth.POST("/verify-email", middleware.RateLimitByIP("auth:verify-email", limit, window), m.Handler.VerifyEmail)
		auth.POST("/verify-email/resend", middleware.RateLimitByIP("auth:verify-email-resend", limit, window), m.Handler.ResendVerification)
		auth.POST("/forgot-password", middleware.RateLimitByIP("auth:forgot-password", limit, window), m.Handler.ForgotPassword)
		auth.POST("/reset-password/verify", middleware.RateLimitByIP("auth:reset-password-verify", limit, window), m.Handler.VerifyResetCode)
		auth.POST("/reset-password", middleware.RateLimitByIP("auth:reset-password", limit, window), m.Handler.ResetPassword)

		// Session routes — a user session only, never an API key
		session := auth.Group("", middleware.JWTAuth(), middleware.DenyAPIKeyAuth())
		session.POST("/logout", m.Handler.Logout)
		session.POST("/logout-all", m.Handler.LogoutAll)
		session.GET("/me", m.Handler.GetMe)
	}

	// The signed-in user's own account: sessions, email, data, closing it.
	// A user session only, never an API key.
	account := router.Group("/account", middleware.JWTAuth(), middleware.DenyAPIKeyAuth())
	{
		account.GET("/sessions", m.Handler.ListSessions)
		account.DELETE("/sessions/:id", m.Handler.EndSession)
		account.POST("/email", m.Handler.RequestEmailChange)
		account.POST("/email/resend", m.Handler.ResendEmailChange)
		account.POST("/email/confirm", m.Handler.ConfirmEmailChange)
		account.POST("/google/link", m.Handler.LinkGoogle)
		account.DELETE("/google", m.Handler.UnlinkGoogle)
		account.GET("/export", m.Handler.ExportAccount)
		// POST, not DELETE: the request carries a body (the re-authentication),
		// which some clients and proxies drop from a DELETE.
		account.POST("/delete", m.Handler.CloseAccount)
	}
}

// StartTokenCleanup deletes expired refresh tokens, emailed codes and
// reset grants every interval until ctx is cancelled. Safe to run on
// every replica: the delete is idempotent.
func (m *AuthModule) StartTokenCleanup(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				deleted, err := m.Service.PurgeExpiredTokens(ctx)
				if err != nil {
					continue // logged by the repository
				}
				if deleted > 0 {
					logger.Info("Expired tokens deleted", logger.Int64("count", deleted))
				}
			}
		}
	}()
}
