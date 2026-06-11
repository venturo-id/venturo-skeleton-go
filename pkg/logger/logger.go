package logger

import (
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log *zap.Logger

// Initialize initializes the global logger
func Initialize(env string) error {
	var config zap.Config

	if env == "production" {
		// Production config: JSON format, Info level
		config = zap.NewProductionConfig()
		config.EncoderConfig.TimeKey = "timestamp"
		config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	} else {
		// Development config: Console format, Debug level
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	// Build logger
	logger, err := config.Build(
		zap.AddCaller(),
		zap.AddCallerSkip(1),
		zap.AddStacktrace(zapcore.ErrorLevel),
	)
	if err != nil {
		return err
	}

	// Optional Discord sink — opt-in via DISCORD_ALERT_WEBHOOK env var.
	// When set, warn/error log entries are tee'd to the webhook with
	// rate-limited batching (see pkg/discord and pkg/logger.discordCore).
	if webhook := os.Getenv("DISCORD_ALERT_WEBHOOK"); webhook != "" {
		level := os.Getenv("DISCORD_ALERT_LEVEL")
		debounce := 10 * time.Second
		if v := os.Getenv("DISCORD_ALERT_DEBOUNCE"); v != "" {
			if d, perr := time.ParseDuration(v); perr == nil && d > 0 {
				debounce = d
			}
		}
		source := os.Getenv("DISCORD_ALERT_SOURCE")
		if source == "" {
			if host, herr := os.Hostname(); herr == nil {
				source = host
			}
		}
		dcore := newDiscordCore(webhook, level, debounce, source)
		logger = logger.WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
			return zapcore.NewTee(c, dcore)
		}))
	}

	Log = logger
	return nil
}

// AttachSentry tees the given core onto the global logger so error-level logs
// are forwarded to Sentry. Called after sentry.Init (Sentry config isn't
// available yet when Initialize runs). Mirrors the Discord sink wired in
// Initialize. No-op if the logger isn't initialized yet.
func AttachSentry(core zapcore.Core) {
	if Log == nil || core == nil {
		return
	}
	Log = Log.WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
		return zapcore.NewTee(c, core)
	}))
}

// GetLogger returns the global logger instance
func GetLogger() *zap.Logger {
	if Log == nil {
		// Fallback: create a basic logger if not initialized
		Log, _ = zap.NewProduction()
	}
	return Log
}

// Sync flushes any buffered log entries
func Sync() {
	if Log != nil {
		_ = Log.Sync()
	}
}

// Helper functions for common logging patterns

func Info(msg string, fields ...zap.Field) {
	GetLogger().Info(msg, fields...)
}

func Debug(msg string, fields ...zap.Field) {
	GetLogger().Debug(msg, fields...)
}

func Error(msg string, fields ...zap.Field) {
	GetLogger().Error(msg, fields...)
}

func Warn(msg string, fields ...zap.Field) {
	GetLogger().Warn(msg, fields...)
}

func Fatal(msg string, fields ...zap.Field) {
	GetLogger().Fatal(msg, fields...)
	os.Exit(1)
}

// WithContext creates a logger with context fields
func WithFields(fields ...zap.Field) *zap.Logger {
	return GetLogger().With(fields...)
}

// Field type wrappers - to avoid importing zap in other packages
type Field = zap.Field

func String(key string, val string) Field {
	return zap.String(key, val)
}

func Int(key string, val int) Field {
	return zap.Int(key, val)
}

func Int32(key string, val int32) Field {
	return zap.Int32(key, val)
}

func Int64(key string, val int64) Field {
	return zap.Int64(key, val)
}

func Float64(key string, val float64) Field {
	return zap.Float64(key, val)
}

func Bool(key string, val bool) Field {
	return zap.Bool(key, val)
}

func Err(err error) Field {
	return zap.Error(err)
}

func Any(key string, val interface{}) Field {
	return zap.Any(key, val)
}
