package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"timebox-be/internal/config"
	"timebox-be/internal/database"
	"timebox-be/internal/router"
	"timebox-be/internal/shared/audit"
	sharedRedis "timebox-be/internal/shared/redis"
	"timebox-be/pkg/jwt"
	"timebox-be/pkg/logger"
	"timebox-be/pkg/validator"

	"github.com/gin-gonic/gin"
)

// Shutdown on SIGTERM, in order. Together (25s) they must stay below the
// pod's terminationGracePeriodSeconds (30s by default).
const (
	// drainDelay is how long the server keeps serving after SIGTERM, with
	// /ready answering 503, before it stops taking requests: the ingress
	// keeps routing to a terminating pod until it has noticed, and each
	// of those requests would otherwise get a 502.
	drainDelay = 5 * time.Second
	// shutdownTimeout is how long in-flight requests then get to finish.
	shutdownTimeout = 15 * time.Second
	// flushTimeout bounds sending the queued emails and writing the
	// queued audit entries, side by side, once the server has stopped.
	flushTimeout = 5 * time.Second
)

// stopSignal is the cause of run's context being cancelled: the signal
// that stops the API.
type stopSignal struct{ os.Signal }

func (s stopSignal) Error() string { return s.String() + " received" }

func init() {
	// Force UTC timezone for the entire application
	// This ensures all time.Now() calls return UTC time
	time.Local = time.UTC
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		// The logger is not set up yet; the message names every bad value.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := logger.Initialize(cfg.Server.Env); err != nil {
		panic("Failed to initialize logger: " + err.Error())
	}
	defer logger.Sync()

	if err := run(cfg); err != nil {
		logger.Fatal("Timebox API stopped", logger.Err(err))
	}
}

func run(cfg *config.Config) error {
	logger.Info("Starting Timebox API", logger.String("env", cfg.Server.Env))

	// Cancelled on SIGTERM (Kubernetes, docker stop) or SIGINT (Ctrl+C),
	// with the signal as its cause; startup and background jobs stop with it.
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		select {
		case sig := <-signals:
			cancel(stopSignal{sig})
		case <-ctx.Done():
		}
	}()

	if err := jwt.ValidateSecret(); err != nil {
		return err
	}

	// Custom binding tags (e.g. `username`) must exist before any request is bound
	if err := validator.RegisterGinValidators(); err != nil {
		return err
	}

	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	logger.Info("Database connected")

	// Redis backs the authz cache and the rate limits; the API does not
	// run without it.
	redisClient, err := sharedRedis.New(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer func() { _ = redisClient.Close() }()
	logger.Info("Redis connected", logger.String("host", cfg.Redis.Host), logger.Int("db", cfg.Redis.DB))

	mail, err := router.NewMailer(cfg)
	if err != nil {
		return err
	}

	// The audit log lives in MongoDB; without MONGO_URI it records nothing.
	auditLog, err := audit.Open(ctx, cfg.Audit)
	if err != nil {
		return err
	}
	if cfg.Audit.Enabled() {
		logger.Info("Audit log connected", logger.String("database", cfg.Audit.Database))
	} else {
		logger.Warn("MONGO_URI is not set — the audit log records nothing")
	}

	if cfg.Server.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New() // middleware is added explicitly in router.Setup

	ready, err := router.Setup(ctx, engine, router.Dependencies{
		DB: db.Pool, Redis: redisClient, Config: cfg, Mailer: mail, Audit: auditLog,
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              ":" + cfg.Server.Port,
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("Listening", logger.String("addr", server.Addr))
		serveErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	// From here a second signal ends the process at once.
	signal.Stop(signals)

	var sig stopSignal
	if errors.As(context.Cause(ctx), &sig) && sig.Signal == syscall.SIGTERM {
		// Ctrl+C has no load balancer to wait for.
		logger.Info("Shutting down: /ready answers 503 while the load balancer stops routing here",
			logger.String("drain_delay", drainDelay.String()))
		ready.Drain()
		time.Sleep(drainDelay)
	}

	logger.Info("Shutting down: finishing in-flight requests")
	var stopErr error
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		// Cut the stragglers off; what they queued is still flushed below.
		logger.Error("Shutdown: in-flight requests did not finish in time", logger.Err(err))
		stopErr = errors.Join(err, server.Close())
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		stopErr = errors.Join(stopErr, err)
	}

	// Emails queued by the last requests (e.g. a reset link) still go out,
	// and queued audit entries are written — each with its own deadline,
	// whatever happened above.
	var flush sync.WaitGroup
	flush.Go(func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), flushTimeout)
		defer cancel()
		if err := mail.Wait(flushCtx); err != nil {
			logger.Warn("Shutdown: some emails were not sent", logger.Err(err))
		}
	})
	flush.Go(func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), flushTimeout)
		defer cancel()
		if err := auditLog.Close(flushCtx); err != nil {
			logger.Warn("Shutdown: some audit log entries were not written", logger.Err(err))
		}
	})
	flush.Wait()

	if stopErr != nil {
		return stopErr
	}
	logger.Info("Shutdown complete")
	return nil
}
