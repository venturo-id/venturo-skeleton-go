// Package sentry wraps github.com/getsentry/sentry-go with the repo's
// conventions: a package-local Config (so we don't import internal/config),
// degraded/no-op behaviour when no DSN is set, and a zapcore.Core that tees
// error-level logs to Sentry (attached from pkg/logger).
//
// The upstream library is imported under the alias sentrygo to avoid clashing
// with this package's name.
package sentry

import (
	"time"

	"venturo-skeleton-go/pkg/logger"

	sentrygo "github.com/getsentry/sentry-go"
)

// Config holds Sentry settings. Mirrors the pkg/email.SMTPConfig pattern: a
// local struct populated by the caller (main.go maps internal/config values
// into it), never importing internal/config.
type Config struct {
	DSN              string
	Environment      string
	Release          string
	TracesSampleRate float64
}

// Enabled reports whether Sentry is configured (a DSN is present).
func (c Config) Enabled() bool { return c.DSN != "" }

// initialized tracks whether sentry.Init succeeded, so helpers can no-op
// cleanly when Sentry is disabled.
var initialized bool

// Init initializes the global Sentry client. When DSN is empty it logs a
// warning and leaves Sentry in no-op mode (not fatal) so the skeleton boots
// without a Sentry account. On success it also attaches the error-tee core to
// the logger.
func Init(cfg Config) error {
	if !cfg.Enabled() {
		logger.Warn("Sentry disabled — SENTRY_DSN not set (running in no-op mode)")
		return nil
	}

	if err := sentrygo.Init(sentrygo.ClientOptions{
		Dsn:              cfg.DSN,
		Environment:      cfg.Environment,
		Release:          cfg.Release,
		TracesSampleRate: cfg.TracesSampleRate,
		EnableTracing:    cfg.TracesSampleRate > 0,
	}); err != nil {
		return err
	}

	initialized = true
	logger.Info("Sentry initialized",
		logger.String("environment", cfg.Environment),
		logger.String("release", cfg.Release),
	)

	// Tee error-level zap logs into Sentry. Mirrors the Discord sink already
	// wired in pkg/logger.
	logger.AttachSentry(NewZapCore())
	return nil
}

// CaptureException reports an error to Sentry. No-op when disabled.
func CaptureException(err error) {
	if !initialized || err == nil {
		return
	}
	sentrygo.CaptureException(err)
}

// CaptureMessage reports a message to Sentry. No-op when disabled.
func CaptureMessage(msg string) {
	if !initialized || msg == "" {
		return
	}
	sentrygo.CaptureMessage(msg)
}

// Flush waits up to timeout for buffered events to be sent. Call on shutdown.
// No-op when disabled.
func Flush(timeout time.Duration) {
	if !initialized {
		return
	}
	sentrygo.Flush(timeout)
}
