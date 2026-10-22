package router

import (
	"context"
	"time"
	"venturo-skeleton-go-simple/internal/config"
	"venturo-skeleton-go-simple/internal/middleware"

	// Core modules
	"venturo-skeleton-go-simple/internal/modules/core/api_key"
	"venturo-skeleton-go-simple/internal/modules/core/auth"
	"venturo-skeleton-go-simple/internal/modules/core/role"
	"venturo-skeleton-go-simple/internal/modules/core/user"
	userRepo "venturo-skeleton-go-simple/internal/modules/core/user/repository"

	"venturo-skeleton-go-simple/internal/shared/authz"
	sharedRedis "venturo-skeleton-go-simple/internal/shared/redis"

	pkgfirebase "venturo-skeleton-go-simple/pkg/firebase"
	"venturo-skeleton-go-simple/pkg/logger"

	"github.com/gin-contrib/cors"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func Setup(router *gin.Engine, db *pgxpool.Pool, cfg *config.Config) {
	// Get logger instance
	log := logger.GetLogger()

	// ─── Redis & authz cache ────────────────────────────────────────
	// Redis backs the per-user permission cache (see
	// internal/shared/authz). It is load-bearing: RequirePermission
	// middleware refuses to answer without it, so a failed bootstrap
	// here is fatal rather than a silent degrade.
	redisClient, err := sharedRedis.New(context.Background(), cfg.Redis)
	if err != nil {
		log.Fatal("Failed to connect to Redis", zap.Error(err))
	}
	log.Info("Redis connected",
		zap.String("addr", cfg.Redis.Host+":"+cfg.Redis.Port),
		zap.Int("db", cfg.Redis.DB),
		zap.Duration("permission_ttl", cfg.Redis.PermissionTTL),
	)

	// Ginzap middleware for logging HTTP requests
	router.Use(ginzap.Ginzap(log, time.RFC3339, true))

	// Recovery middleware with Zap (handles panics)
	router.Use(ginzap.RecoveryWithZap(log, true))

	// CORS middleware configuration
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5173", "http://localhost:3000", "http://localhost:3001", "http://localhost:8081", "https://app.tuai.id", "https://jesuit.venturo.pro", "https://skeleton.venturo.id"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-API-Key"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// Health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"message": "Tuai API is running",
		})
	})

	// Core v1 routes
	coreV1 := router.Group("/core/v1")
	{
		// Initialize and setup auth module
		authModule := auth.Initialize(db, cfg)
		authModule.SetupRoutes(coreV1)

		// Wire the user_identities repo (used by Google sign-in to look
		// up / link Firebase identities to a core.users row).
		authModule.Service.SetUserIdentityRepo(userRepo.NewUserIdentityRepository(db))

		// Wire the Firebase Admin client when configured. If the
		// operator hasn't set FIREBASE_PROJECT_ID the verifier stays
		// nil; the /auth/google endpoint will then surface a 503
		// "Google sign-in is not configured" instead of crashing.
		if cfg.Firebase.ProjectID != "" {
			fbClient, err := pkgfirebase.New(context.Background(), cfg.Firebase.ProjectID, cfg.Firebase.CredentialsJSON)
			if err != nil {
				log.Fatal("Failed to initialize Firebase Admin", zap.Error(err))
			}
			authModule.Service.SetFirebaseVerifier(fbClient)
			log.Info("Firebase Admin initialized", zap.String("project_id", cfg.Firebase.ProjectID))
		} else {
			log.Warn("FIREBASE_PROJECT_ID not set — /auth/google endpoint disabled")
		}

		// Initialize and setup user module
		userModule := user.Initialize(db)
		userModule.SetupRoutes(coreV1)

		// Initialize and setup role module
		roleModule := role.Initialize(db)
		roleModule.SetupRoutes(coreV1)

		// Wire the permission cache now that both Redis and the role repo
		// exist. The role repo acts as the authoritative fetcher on cache
		// miss; role and user services use the same cache to invalidate
		// stale entries when role permissions or assignments change; auth
		// service uses it to serve /auth/me without hitting the DB on every
		// call.
		authzService := authz.NewService(redisClient, roleModule.Repository, cfg.Redis.PermissionTTL)
		middleware.SetAuthzService(authzService)
		roleModule.Service.SetPermissionCacheInvalidator(authzService)
		authModule.Service.SetPermissionReader(authzService)
		authModule.Service.SetRoleRepo(roleModule.Repository)

		// Wire role assignment into the user service: create/update with
		// role_id writes core.user_roles and drops the user's cached set.
		userModule.Service.SetRoleLookup(roleModule.Repository)
		userModule.Service.SetPermissionCacheInvalidator(authzService)

		// Initialize and setup API key module
		apiKeyModule := api_key.Initialize(db, roleModule.Repository)
		apiKeyModule.SetupRoutes(coreV1)

		// Wire up API key validator for middleware
		middleware.SetApiKeyValidator(apiKeyModule.Service)
	}

	log.Info("Routes setup completed", zap.Int("routes", len(router.Routes())))
}
