package main

import (
	"time"

	"venturo-skeleton-go/internal/config"
	"venturo-skeleton-go/internal/database"
	"venturo-skeleton-go/internal/router"
	"venturo-skeleton-go/pkg/jwt"
	"venturo-skeleton-go/pkg/logger"
	"venturo-skeleton-go/pkg/sentry"

	"github.com/gin-gonic/gin"
)

func init() {
	// Force UTC timezone for the entire application
	// This ensures all time.Now() calls return UTC time
	time.Local = time.UTC
}

func main() {
	// Load configuration
	cfg := config.Load()

	// Initialize logger
	if err := logger.Initialize(cfg.Server.Env); err != nil {
		panic("Failed to initialize logger: " + err.Error())
	}
	defer logger.Sync()

	logger.Info("Starting Tuai API")

	// Initialize Sentry (degraded/no-op when SENTRY_DSN is empty). Must come
	// after logger.Initialize: it attaches an error-tee core to the logger.
	if err := sentry.Init(sentry.Config{
		DSN:              cfg.Sentry.DSN,
		Environment:      cfg.Sentry.Environment,
		Release:          cfg.Sentry.Release,
		TracesSampleRate: cfg.Sentry.TracesSampleRate,
	}); err != nil {
		logger.Error("Sentry init failed (continuing without Sentry)", logger.Err(err))
	}
	defer sentry.Flush(2 * time.Second)

	// Validate JWT secret configuration
	if err := jwt.ValidateSecret(cfg.Server.Env); err != nil {
		logger.Fatal("JWT secret validation failed: " + err.Error())
	}
	logger.Info("JWT secret validation passed")

	// Initialize database
	db, err := database.New(cfg.Database.GetDSN())
	if err != nil {
		logger.Fatal("Failed to connect to database")
	}
	defer db.Close()

	logger.Info("Database connected successfully")

	// Initialize Gin router
	ginRouter := gin.New() // Use gin.New() instead of gin.Default() since we use custom middleware

	// Setup routes (all modules and shared infra initialized inside router)
	router.Setup(ginRouter, db.Pool, cfg)

	// Start server
	serverAddr := ":" + cfg.Server.Port
	logger.Info("Starting server on " + serverAddr + " (Environment: " + cfg.Server.Env + ")")

	if err := ginRouter.Run(serverAddr); err != nil {
		logger.Fatal("Failed to start server")
	}
}
