package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"timebox-be/internal/config"
	"timebox-be/internal/middleware"

	// Core modules
	"timebox-be/internal/modules/core/apikey"
	"timebox-be/internal/modules/core/auditlog"
	"timebox-be/internal/modules/core/auth"
	authHandler "timebox-be/internal/modules/core/auth/handler"
	authService "timebox-be/internal/modules/core/auth/service"
	"timebox-be/internal/modules/core/role"
	roleRepo "timebox-be/internal/modules/core/role/repository"
	"timebox-be/internal/modules/core/user"
	userRepo "timebox-be/internal/modules/core/user/repository"

	"timebox-be/internal/shared/audit"
	"timebox-be/internal/shared/authz"
	"timebox-be/internal/shared/mailer"
	"timebox-be/internal/shared/ratelimit"

	"timebox-be/pkg/avatar"
	"timebox-be/pkg/email"
	"timebox-be/pkg/googleauth"
	"timebox-be/pkg/logger"
	"timebox-be/pkg/storage"

	"github.com/gin-contrib/cors"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

// maxBodyBytes caps every request body. JSON payloads stay far below it;
// the one upload through the API, a profile photo, is cropped to 512px by
// the app first. Larger files go straight to storage through presigned
// URLs.
const maxBodyBytes = 2 << 20 // 2 MiB

// refreshTokenCleanupInterval is how often expired refresh tokens are deleted.
const refreshTokenCleanupInterval = time.Hour

// Dependencies is the infrastructure the modules are wired on.
type Dependencies struct {
	DB     *pgxpool.Pool
	Redis  *goredis.Client
	Config *config.Config
	// Mailer sends in the background; wait for it on shutdown.
	Mailer *mailer.Async
	// Audit is the audit log (audit.Discard without MONGO_URI); close it
	// on shutdown.
	Audit audit.Log
}

// NewMailer builds the email sender: SMTP when configured, otherwise one
// that only logs (at debug level, with the codes, for development).
// Email verification cannot be required without SMTP.
func NewMailer(cfg *config.Config) (*mailer.Async, error) {
	if !cfg.Email.Enabled() {
		if cfg.Security.EmailVerificationRequired {
			return nil, errors.New("EMAIL_VERIFICATION_REQUIRED=true needs SMTP (at least SMTP_HOST and SMTP_FROM_EMAIL)")
		}
		logger.Warn("SMTP is not configured — emails are only logged at debug level")
		return mailer.NewAsync(&email.NoOpEmailService{}), nil
	}

	smtp, err := email.NewSMTPEmailService(cfg.Email)
	if err != nil {
		return nil, err
	}
	return mailer.NewAsync(smtp), nil
}

// Setup registers the middleware and every module's routes on engine.
// Background jobs it starts stop when ctx is cancelled. Drain the returned
// Readiness before shutting the server down.
func Setup(ctx context.Context, engine *gin.Engine, deps Dependencies) (*Readiness, error) {
	cfg := deps.Config
	log := logger.GetLogger()

	// Only the configured proxies may set X-Forwarded-For. Gin trusts every
	// proxy by default, which lets any client spoof its IP past API-key IP
	// whitelists and per-IP rate limits.
	if err := engine.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		return nil, fmt.Errorf("invalid TRUSTED_PROXIES: %w", err)
	}
	if len(cfg.Server.TrustedProxies) == 0 && cfg.Server.IsProduction() {
		logger.Warn("TRUSTED_PROXIES is empty — behind an ingress every client shares the ingress IP for rate limits and API-key whitelists")
	}

	// Shared Redis-backed counter for per-IP auth limits and per-key API limits
	limiter := ratelimit.New(deps.Redis)
	middleware.SetRateLimiter(limiter)

	engine.Use(ginzap.GinzapWithConfig(log, &ginzap.Config{
		TimeFormat: time.RFC3339,
		UTC:        true,
		SkipPaths:  []string{"/health", "/ready"},
	}))
	engine.Use(middleware.Recovery())
	engine.Use(middleware.AuditClient())
	if len(cfg.Server.CORSAllowedOrigins) > 0 {
		engine.Use(cors.New(cors.Config{
			AllowOrigins:     cfg.Server.CORSAllowedOrigins,
			AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-API-Key", authHandler.ClientHeader},
			ExposeHeaders:    []string{"Content-Length", "Retry-After"},
			AllowCredentials: true,
			MaxAge:           12 * time.Hour,
		}))
	} else {
		logger.Warn("CORS_ALLOWED_ORIGINS is empty — browsers on other origins cannot call the API")
	}
	engine.Use(middleware.MaxBodySize(maxBodyBytes))

	// Liveness: the process is up, until it exits. Readiness: it can reach
	// its stores and is not draining for shutdown.
	engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	ready := newReadiness(postgresStore(deps.DB), redisStore(deps.Redis))
	engine.GET("/ready", ready.handle)

	// ─── Shared repositories & authz ────────────────────────────────
	// The role repository is the source of truth for the authz cache;
	// JWTAuth reads that cache on every request.
	users := userRepo.NewUserRepository(deps.DB)
	roles := roleRepo.NewRoleRepository(deps.DB)
	authzService := authz.NewService(deps.Redis, roles, cfg.Redis.PermissionTTL)
	middleware.SetAuthzService(authzService)

	// Google sign-in is optional per platform: one without a client
	// answers 503.
	google, err := googleauth.New(ctx, cfg.Google)
	if err != nil {
		return nil, fmt.Errorf("initialize Google sign-in: %w", err)
	}
	for platform, client := range map[string]googleauth.Client{
		googleauth.PlatformDesktop: cfg.Google.Desktop, googleauth.PlatformWeb: cfg.Google.Web,
	} {
		if !client.Enabled() {
			logger.Warn("Google sign-in not configured", logger.String("platform", platform))
		}
	}

	avatars, err := newAvatarStore(cfg.Storage)
	if err != nil {
		return nil, err
	}

	// ─── Modules ────────────────────────────────────────────────────
	apiKeyModule := apikey.Initialize(deps.DB, authzService, limiter, deps.Audit)
	middleware.SetAPIKeyValidator(apiKeyModule.Service)

	authModule := auth.Initialize(deps.DB, cfg, auth.Dependencies{
		Users:    users,
		Roles:    roles,
		Subjects: authzService,
		APIKeys:  apiKeyModule.Service,
		Limiter:  limiter,
		Audit:    deps.Audit,
		Mailer:   deps.Mailer,
		Google:   google,
		Avatars:  avatarFiles(avatars),
	})
	// Deactivating or deleting a user ends the user's sessions through the
	// auth service, which also does the caller's password change.
	userModule := user.Initialize(deps.DB, users, roles, authzService, authModule.Service, deps.Audit, avatars)
	roleModule := role.Initialize(roles, authzService, deps.Audit)
	auditLogModule := auditlog.Initialize(deps.Audit)

	coreV1 := engine.Group("/core/v1")
	authModule.SetupRoutes(coreV1)
	userModule.SetupRoutes(coreV1)
	roleModule.SetupRoutes(coreV1)
	apiKeyModule.SetupRoutes(coreV1)
	auditLogModule.SetupRoutes(coreV1)

	authModule.StartTokenCleanup(ctx, refreshTokenCleanupInterval)

	logger.Info("Routes setup completed", logger.Int("routes", len(engine.Routes())))
	return ready, nil
}

// newAvatarStore builds the profile photo store on R2. Photos are served
// from R2_PUBLIC_URL, so without it (or without R2) there is none: the
// avatar endpoints answer 503 and Google pictures are not copied.
func newAvatarStore(cfg storage.Config) (*avatar.Store, error) {
	if !cfg.Enabled() || cfg.PublicURL == "" {
		logger.Warn("R2 storage or R2_PUBLIC_URL not configured — profile photos disabled")
		return nil, nil
	}
	r2, err := storage.NewR2(cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize R2 storage: %w", err)
	}
	logger.Info("Profile photos stored in R2", logger.String("public_url", cfg.PublicURL))
	return avatar.NewStore(r2, cfg.PublicURL), nil
}

// avatarStore is the avatar store as the auth module uses it: copying
// Google profile pictures in, and deleting a closed account's photo.
type avatarStore struct {
	store *avatar.Store
}

// avatarFiles is the auth module's photo storage, or nil (not a typed
// nil) without a store.
func avatarFiles(store *avatar.Store) authService.Avatars {
	if store == nil {
		return nil
	}
	return avatarStore{store: store}
}

func (a avatarStore) ImportGooglePicture(ctx context.Context, userID, pictureURL string) (string, error) {
	photo, err := avatar.FetchGooglePicture(ctx, pictureURL)
	if err != nil {
		return "", err
	}
	return a.store.Put(ctx, userID, photo)
}

func (a avatarStore) Delete(ctx context.Context, avatarURL string) error {
	return a.store.Delete(ctx, avatarURL)
}
