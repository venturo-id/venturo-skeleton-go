package router

import (
	"context"
	"net/http"
	"time"
	"venturo-skeleton-go/internal/config"
	"venturo-skeleton-go/internal/middleware"

	// Core modules
	"venturo-skeleton-go/internal/modules/core/api_key"
	"venturo-skeleton-go/internal/modules/core/approval"
	"venturo-skeleton-go/internal/modules/core/auth"
	"venturo-skeleton-go/internal/modules/core/branch"
	"venturo-skeleton-go/internal/modules/core/client"
	"venturo-skeleton-go/internal/modules/core/company"
	"venturo-skeleton-go/internal/modules/core/role"
	"venturo-skeleton-go/internal/modules/core/translation_overrides"
	"venturo-skeleton-go/internal/modules/core/user"
	userRepo "venturo-skeleton-go/internal/modules/core/user/repository"

	"venturo-skeleton-go/internal/shared/audit"
	"venturo-skeleton-go/internal/shared/authz"
	"venturo-skeleton-go/internal/shared/rabbitmq"
	sharedRedis "venturo-skeleton-go/internal/shared/redis"
	"venturo-skeleton-go/internal/shared/response"
	"venturo-skeleton-go/internal/shared/tokendenylist"

	"venturo-skeleton-go/pkg/cache"
	pkgfirebase "venturo-skeleton-go/pkg/firebase"
	"venturo-skeleton-go/pkg/logger"
	"venturo-skeleton-go/pkg/notification"
	pkgsentry "venturo-skeleton-go/pkg/sentry"
	"venturo-skeleton-go/pkg/storage"

	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-contrib/cors"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func Setup(router *gin.Engine, db *pgxpool.Pool, cfg *config.Config) {
	// Get logger instance
	log := logger.GetLogger()

	// Sentry is active when a DSN is configured (see pkg/sentry). Computed
	// once here from cfg — used by the recovery middleware and the dev-only
	// test endpoint below.
	sentryActive := cfg.Sentry.DSN != ""
	// Gate internal error detail by environment for the centralized error
	// renderer (response.RenderError). Generic messages in production; detail
	// allowed in dev/staging. See docs/errors.md.
	response.Configure(cfg.Server.Env)

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

	// ─── Access-token denylist (revocation) ─────────────────────────
	// Makes JWT access tokens revocable on logout / logout-all. Shares
	// the Redis client above. Wired into the JWTAuth middleware (reads)
	// and the auth service (writes, below). Fail-open by design — a
	// Redis outage degrades to "no revocation", never an auth lockout.
	tokenDenylistService := tokendenylist.NewService(redisClient)
	middleware.SetTokenDenylist(tokenDenylistService)

	// ─── Cache (reuses the Redis client above) ──────────────────────
	// The typed cache shares the existing Redis connection — it never opens
	// a new one. Provided for modules to inject via setter (like authzService).
	cacheService := cache.New(redisClient, cache.Config{
		KeyPrefix:  cfg.Cache.KeyPrefix,
		DefaultTTL: cfg.Cache.DefaultTTL,
	})
	_ = cacheService // not yet wired to a business module
	log.Info("Cache initialized",
		zap.String("key_prefix", cfg.Cache.KeyPrefix),
		zap.Duration("default_ttl", cfg.Cache.DefaultTTL),
	)

	// ─── Storage (degraded: nil when no credentials configured) ─────
	storageClient := initStorage(context.Background(), cfg)
	_ = storageClient // not yet wired to a business module

	// ─── Notification (always succeeds; channels degrade to no-op) ──
	// notification.New logs the selected adapters at boot.
	notifier, err := notification.New(context.Background(), notification.Config{
		Twilio: notification.TwilioConfig{
			AccountSID: cfg.Notification.TwilioAccountSID,
			AuthToken:  cfg.Notification.TwilioAuthToken,
			FromNumber: cfg.Notification.TwilioFromNumber,
		},
		FCM: notification.FCMConfig{
			CredentialsJSON: cfg.Notification.FCMCredentialsJSON,
		},
	}, notification.NewEmailSender())
	if err != nil {
		log.Error("Notification init failed (continuing with no-op)", zap.Error(err))
	}
	_ = notifier // not yet wired to a business module

	// Ginzap middleware for logging HTTP requests
	router.Use(ginzap.Ginzap(log, time.RFC3339, true))

	// Sentry middleware — captures panics before the zap recovery below
	// re-recovers them. Only mounted when Sentry is active. Repanic:true so
	// the existing RecoveryWithZap still produces the 500 response.
	if sentryActive {
		router.Use(sentrygin.New(sentrygin.Options{Repanic: true}))
	}

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

	// ─── RabbitMQ client + publisher + consumer (degraded/opt-in) ───
	// Messaging is optional for the skeleton: when RABBITMQ_ENABLED=false, or
	// the broker is unreachable, the app boots without messaging (warn, not
	// fatal) and the publisher/consumer are simply not wired. Flip to
	// load-bearing once a module actually depends on the queue.
	if !cfg.RabbitMQ.Enabled {
		log.Warn("RabbitMQ disabled (RABBITMQ_ENABLED=false) — messaging not started")
	} else if rabbitClient, err := rabbitmq.New(context.Background(), cfg.RabbitMQ); err != nil {
		log.Warn("RabbitMQ unavailable — messaging disabled, app continues", zap.Error(err))
	} else {
		// Build the publisher (available for future module injection) and start
		// the example consumer as an end-to-end proof. The consumer
		// re-subscribes automatically across reconnects.
		publisher, err := rabbitmq.NewPublisher(rabbitClient, cfg.RabbitMQ)
		if err != nil {
			log.Warn("RabbitMQ publisher setup failed — messaging disabled", zap.Error(err))
		} else {
			_ = publisher // not yet wired to a business module

			consumer := rabbitmq.NewConsumer(rabbitClient, cfg.RabbitMQ)
			rabbitmq.RegisterExampleConsumer(consumer)
			if err := consumer.Start(context.Background()); err != nil {
				log.Warn("RabbitMQ consumer start failed — messaging degraded", zap.Error(err))
			}
		}
	}

	// ─── Dev-only Sentry test endpoint ──────────────────────────────
	// Proves error tracking end-to-end. Only mounted in development.
	if cfg.Server.Env == "development" && sentryActive {
		router.GET("/debug/sentry-test", func(c *gin.Context) {
			pkgsentry.CaptureMessage("sentry-test endpoint hit")
			logger.Error("sentry-test: synthetic error log")
			c.JSON(http.StatusOK, gin.H{"message": "sent test event to Sentry"})
		})
	}

	// Core v1 routes
	coreV1 := router.Group("/core/v1")
	{
		// Initialize and setup client module. Must come before auth so
		// that SignUp can provision a client for new registrants.
		clientModule := client.Initialize(db)
		clientModule.SetupRoutes(coreV1)

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
		// miss; role service uses the same cache to invalidate stale
		// entries when role permissions or assignments change; auth service
		// uses it to serve /auth/me without hitting the DB on every call.
		authzService := authz.NewService(redisClient, roleModule.Repository, cfg.Redis.PermissionTTL)
		middleware.SetAuthzService(authzService)
		roleModule.Service.SetPermissionCacheInvalidator(authzService)
		authModule.Service.SetPermissionReader(authzService)

		// Wire the access-token denylist write side so Logout /
		// LogoutAll can revoke the access token, not just the refresh
		// token (read side wired into the middleware above).
		authModule.Service.SetTokenDenylist(tokenDenylistService)

		// Initialize and setup company module
		companyModule := company.Initialize(db)
		companyModule.SetupRoutes(coreV1)
		companyModule.SetupUserCompanyRoutes(coreV1)

		// Initialize and setup branch module
		branchModule := branch.Initialize(db)
		branchModule.SetupRoutes(coreV1)
		branchModule.SetupUserBranchRoutes(coreV1)

		// Wire up auth service dependencies for company operations
		authModule.Service.SetCompanyUserRepo(companyModule.UserRepository)
		authModule.Service.SetCompanyRepo(companyModule.Repository)
		authModule.Service.SetRoleRepo(roleModule.Repository)
		authModule.Service.SetBranchRepo(branchModule.Repository)
		authModule.Service.SetClientService(clientModule.Service)

		// Wire the client lookup into company service so Create can
		// resolve client_id for owners who have no primary company yet.
		companyModule.Service.SetClientLookup(clientModule.Repository)

		// Wire branch repo into company service so Create seeds a default branch
		companyModule.Service.SetBranchRepo(branchModule.Repository)

		// Wire default admin role so the creator gets full company-scoped
		// permissions out of the box (mirrors SignUp).
		companyModule.Service.SetDefaultAdminRoleID(cfg.Auth.DefaultAdminRoleID)

		// Wire the company-scope resolver into the user-branch service so
		// non-super-admin callers can only assign branches inside their
		// company subtree.
		branchModule.UserBranchService.SetScopeResolver(companyModule.Repository)

		// Wire the company membership lookup into the branch service so
		// /branches/by-companies can resolve non-super_admin callers'
		// allowed companies from core.company_users.
		branchModule.Service.SetCompanyMembershipLookup(companyModule.UserRepository)

		// Wire up user service dependencies for company sync on create
		userModule.Service.SetCompanySyncer(companyModule.Service)
		userModule.Service.SetBranchSyncer(branchModule.UserBranchService)
		userModule.Service.SetRoleLookup(roleModule.Repository)

		// Wire the live company-context verifier so that any endpoint guarded
		// by middleware.CompanyContext rejects stale JWT company claims (e.g.
		// user removed from the company, company soft-deleted/deactivated).
		middleware.SetCompanyContextVerifier(companyModule.UserRepository)

		// User module routes only run JWTAuth (not CompanyContext) because
		// super_admin paths must remain accessible without a tenant. Inject
		// the verifier directly into the user handler so its inline tenant
		// scope check enforces the same staleness guarantees.
		userModule.Handler.SetCompanyVerifier(companyModule.UserRepository)

		// Wire the branch-scope resolver so middleware.BranchScope() can
		// narrow branches reads to the caller's user_branches set.
		middleware.SetUserBranchResolver(branchModule.UserBranchRepository)

		// Initialize and setup API key module
		apiKeyModule := api_key.Initialize(db, roleModule.Repository)
		apiKeyModule.SetupRoutes(coreV1)

		// Wire up API key validator for middleware
		middleware.SetApiKeyValidator(apiKeyModule.Service)

		// Initialize and setup Approval module (depends on role repo for
		// snapshotting role names at submission time).
		approvalModule := approval.Initialize(db, roleModule.Repository)
		approvalModule.SetupRoutes(coreV1)

		// Initialize and setup Translation Overrides module. Exposes both
		// an unauthenticated bootstrap endpoint (resolves client by slug)
		// and super_admin CRUD nested under /admin/clients/:client_id.
		translationOverridesModule := translation_overrides.Initialize(db, clientModule.Service)
		translationOverridesModule.SetupRoutes(coreV1)
	}

	// Shared audit module (polymorphic, used by any feature that wants
	// per-entity history). Initialized after the core group so its routes
	// mount under /core/v1.
	auditModule := audit.Initialize(db)
	auditModule.SetupRoutes(coreV1)

	log.Info("Routes setup completed", zap.Int("routes", len(router.Routes())))
}

// initStorage builds a storage client for the configured provider, returning
// nil (degraded) when the provider has no usable credentials so the app still
// boots without cloud storage.
func initStorage(ctx context.Context, cfg *config.Config) storage.Client {
	provider := cfg.Storage.Provider
	switch provider {
	case "", "gcs":
		if cfg.GCS.BucketName == "" {
			logger.Warn("Storage disabled — GCS_BUCKET_NAME not set (running without storage)")
			return nil
		}
	case "s3", "minio":
		if cfg.Storage.S3Bucket == "" {
			logger.Warn("Storage disabled — S3_BUCKET not set (running without storage)")
			return nil
		}
	}

	client, err := storage.New(ctx, storage.Config{
		Provider:           provider,
		GCSBucket:          cfg.GCS.BucketName,
		GCSCredentialsJSON: cfg.GCS.CredentialsJSON,
		S3Endpoint:         cfg.Storage.S3Endpoint,
		S3Region:           cfg.Storage.S3Region,
		S3Bucket:           cfg.Storage.S3Bucket,
		S3AccessKeyID:      cfg.Storage.S3AccessKeyID,
		S3SecretAccessKey:  cfg.Storage.S3SecretAccessKey,
		S3PublicBaseURL:    cfg.Storage.S3PublicBaseURL,
		S3UsePathStyle:     cfg.Storage.S3UsePathStyle,
	})
	if err != nil {
		logger.Error("Storage init failed (continuing without storage)", logger.Err(err))
		return nil
	}
	return client
}
