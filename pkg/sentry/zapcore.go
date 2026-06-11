package sentry

import (
	sentrygo "github.com/getsentry/sentry-go"
	"go.uber.org/zap/zapcore"
)

// sentryCore is a zapcore.Core that forwards entries at or above ErrorLevel to
// Sentry. It does not encode or write the entry anywhere else — pair with
// zapcore.NewTee so the primary core still writes to stdout/file. Mirrors
// pkg/logger.discordCore.
type sentryCore struct {
	minLevel zapcore.Level
	fields   []zapcore.Field
}

// NewZapCore returns a zapcore.Core that tees error-level logs to Sentry.
// Returned to pkg/logger via logger.AttachSentry.
func NewZapCore() zapcore.Core {
	return &sentryCore{minLevel: zapcore.ErrorLevel}
}

func (c *sentryCore) Enabled(lvl zapcore.Level) bool {
	return lvl >= c.minLevel
}

func (c *sentryCore) With(fields []zapcore.Field) zapcore.Core {
	clone := *c
	if len(fields) > 0 {
		merged := make([]zapcore.Field, 0, len(c.fields)+len(fields))
		merged = append(merged, c.fields...)
		merged = append(merged, fields...)
		clone.fields = merged
	}
	return &clone
}

func (c *sentryCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *sentryCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range c.fields {
		f.AddTo(enc)
	}
	for _, f := range fields {
		f.AddTo(enc)
	}

	hub := sentrygo.CurrentHub().Clone()
	hub.WithScope(func(s *sentrygo.Scope) {
		// Pack the structured log fields into a single Sentry context block.
		if len(enc.Fields) > 0 {
			s.SetContext("log_fields", sentrygo.Context(enc.Fields))
		}
		s.SetLevel(zapToSentryLevel(ent.Level))
		if ent.Caller.Defined {
			s.SetTag("caller", ent.Caller.TrimmedPath())
		}
		hub.CaptureMessage(ent.Message)
	})
	return nil
}

func (c *sentryCore) Sync() error { return nil }

func zapToSentryLevel(lvl zapcore.Level) sentrygo.Level {
	switch {
	case lvl >= zapcore.FatalLevel:
		return sentrygo.LevelFatal
	case lvl >= zapcore.ErrorLevel:
		return sentrygo.LevelError
	case lvl >= zapcore.WarnLevel:
		return sentrygo.LevelWarning
	default:
		return sentrygo.LevelInfo
	}
}
