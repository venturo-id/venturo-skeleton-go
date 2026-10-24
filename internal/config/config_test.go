package config

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGetDSN_EscapesCredentials(t *testing.T) {
	cfg := DatabaseConfig{
		Host: "db.internal", Port: "5432", User: "app",
		Password: "p@ss:w/rd#?%1", DBName: "timebox", SSLMode: "require",
	}

	parsed, err := url.Parse(cfg.GetDSN())
	if err != nil {
		t.Fatalf("GetDSN() is not a valid URL: %v", err)
	}
	if got, _ := parsed.User.Password(); got != cfg.Password {
		t.Errorf("password = %q, want %q", got, cfg.Password)
	}
	if parsed.Host != "db.internal:5432" || parsed.Path != "/timebox" {
		t.Errorf("host/path = %q %q, want db.internal:5432 /timebox", parsed.Host, parsed.Path)
	}
	if q := parsed.Query(); q.Get("sslmode") != "require" || q.Get("timezone") != "UTC" {
		t.Errorf("query = %v, want sslmode=require timezone=UTC", q)
	}
}

func TestGetEnv_TrimsMakeInlineCommentPadding(t *testing.T) {
	// `make` keeps the spaces before an inline .env comment in the value.
	t.Setenv("TEST_INT", "3                               ")
	t.Setenv("TEST_DURATION", " 45s ")
	t.Setenv("TEST_BOOL", "true   ")

	var l loader
	if got := l.int("TEST_INT", 10); got != 3 {
		t.Errorf("int() = %d, want 3", got)
	}
	if got := l.duration("TEST_DURATION", time.Minute); got != 45*time.Second {
		t.Errorf("duration() = %s, want 45s", got)
	}
	if got := l.bool("TEST_BOOL", false); !got {
		t.Errorf("bool() = false, want true")
	}
	if len(l.errs) != 0 {
		t.Errorf("errors = %v, want none", l.errs)
	}
}

func TestLoad_RefusesUnreadableValues(t *testing.T) {
	// Each of these used to fall back to its default without a word.
	t.Setenv("EMAIL_VERIFICATION_REQUIRED", "yes")
	t.Setenv("ACCOUNT_LOCKOUT_DURATION", "30")
	t.Setenv("JWT_REFRESH_EXPIRATION", "7d")
	t.Setenv("JWT_EXPIRATION", "15")
	t.Setenv("REDIS_DB", "one")
	t.Setenv("ENV", "prod")
	t.Setenv("DB_SSLMODE", "prefer")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() accepted unreadable values")
	}
	for _, key := range []string{
		"EMAIL_VERIFICATION_REQUIRED", "ACCOUNT_LOCKOUT_DURATION", "JWT_REFRESH_EXPIRATION",
		"JWT_EXPIRATION", "REDIS_DB", "ENV", "DB_SSLMODE",
	} {
		if !strings.Contains(err.Error(), key+"=") {
			t.Errorf("error does not name %s: %v", key, err)
		}
	}
}

func TestLoad_AcceptsValidValues(t *testing.T) {
	t.Setenv("EMAIL_VERIFICATION_REQUIRED", "true")
	t.Setenv("ACCOUNT_LOCKOUT_DURATION", "45m")
	t.Setenv("ENV", "staging")
	t.Setenv("DB_SSLMODE", "verify-full")
	t.Setenv("REDIS_DB", "2")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Security.EmailVerificationRequired || cfg.Security.AccountLockoutDuration != 45*time.Minute ||
		cfg.Server.Env != "staging" || cfg.Database.SSLMode != "verify-full" || cfg.Redis.DB != 2 {
		t.Errorf("Load() = %+v", cfg)
	}
}
