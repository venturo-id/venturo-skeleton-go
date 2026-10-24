package logger

import (
	"strings"
	"time"

	"timebox-be/pkg/discord"

	"go.uber.org/zap/zapcore"
)

// discordCore is a zapcore.Core that forwards entries at or above minLevel
// to a Discord webhook via a rate-limited notifier. It does not encode or
// write the entry anywhere else — pair with NewTee so the primary core
// still writes to stdout/file.
type discordCore struct {
	minLevel zapcore.Level
	notifier *discord.Notifier
	fields   []zapcore.Field
}

func newDiscordCore(webhookURL, levelStr string, debounce time.Duration, source string) zapcore.Core {
	lvl := zapcore.WarnLevel
	switch strings.ToLower(strings.TrimSpace(levelStr)) {
	case "error":
		lvl = zapcore.ErrorLevel
	case "warn", "warning", "":
		lvl = zapcore.WarnLevel
	}
	return &discordCore{
		minLevel: lvl,
		notifier: discord.NewNotifier(webhookURL, debounce, source),
	}
}

func (c *discordCore) Enabled(lvl zapcore.Level) bool {
	return lvl >= c.minLevel
}

func (c *discordCore) With(fields []zapcore.Field) zapcore.Core {
	clone := *c
	if len(fields) > 0 {
		merged := make([]zapcore.Field, 0, len(c.fields)+len(fields))
		merged = append(merged, c.fields...)
		merged = append(merged, fields...)
		clone.fields = merged
	}
	return &clone
}

func (c *discordCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *discordCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range c.fields {
		f.AddTo(enc)
	}
	for _, f := range fields {
		f.AddTo(enc)
	}

	c.notifier.Notify(discord.Event{
		Level:   ent.Level.String(),
		Message: ent.Message,
		Caller:  ent.Caller.TrimmedPath(),
		Time:    ent.Time,
		Fields:  enc.Fields,
	})
	return nil
}

func (c *discordCore) Sync() error { return nil }
