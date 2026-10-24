package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"timebox-be/internal/shared/audit"
	"timebox-be/pkg/email"
	"timebox-be/pkg/googleauth"
	"timebox-be/pkg/storage"
)

type Config struct {
	Database  DatabaseConfig
	Server    ServerConfig
	Security  SecurityConfig
	RateLimit RateLimitConfig
	Redis     RedisConfig
	// Google holds the OAuth clients of Google sign-in, one per platform;
	// a platform without one answers 503.
	Google googleauth.Config
	// Email is the SMTP account and what emails show; build the
	// sender with email.NewSMTPEmailService when Email.Enabled().
	Email email.Config
	// Storage is the Cloudflare R2 bucket; build the client with
	// storage.NewR2 when Storage.Enabled().
	Storage storage.Config
	// Audit is the MongoDB database of the audit log; audit.Open gives a
	// log that records nothing when Audit.Enabled() is false.
	Audit audit.Config
}

type DatabaseConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string

	// Connection pool. Zero keeps the pgx default.
	MaxConns          int
	MinConns          int
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	ConnectTimeout    time.Duration
}

type ServerConfig struct {
	Port string
	Env  string
	// TrustedProxies are the IPs/CIDRs (e.g. the ingress controller) allowed
	// to set X-Forwarded-For. Empty trusts no proxy: the client IP is the
	// TCP peer. Client IPs feed API-key IP whitelists and rate limits.
	TrustedProxies []string
	// CORSAllowedOrigins are the browser origins allowed to call the API
	// with credentials, e.g. https://app.timebox.id.
	CORSAllowedOrigins []string
}

// IsProduction reports whether the server runs with ENV=production.
func (c ServerConfig) IsProduction() bool {
	return c.Env == "production"
}

// RateLimitConfig bounds unauthenticated auth endpoints per client IP.
type RateLimitConfig struct {
	AuthLimit  int
	AuthWindow time.Duration
}

type SecurityConfig struct {
	EmailVerificationRequired bool
	MaxLoginAttempts          int
	AccountLockoutDuration    time.Duration
	RefreshTokenExpiration    time.Duration

	// Emailed one-time codes (email verification, password reset, email
	// change): how long one works, how many wrong guesses kill it, and how
	// long to wait before another is sent to the same address.
	OTPExpiration     time.Duration
	OTPMaxAttempts    int
	OTPResendCooldown time.Duration
	// ResetTokenExpiration is how long the reset token a password reset
	// code is traded for works.
	ResetTokenExpiration time.Duration
	// Emails of each kind one account may be sent per hour; requests
	// beyond that are answered as usual but send nothing.
	MaxVerificationEmails  int
	MaxPasswordResetEmails int
}

type RedisConfig struct {
	Host     string
	Port     string
	Password string
	DB       int
	// PermissionTTL controls how long a user's effective permission set is
	// cached before being re-fetched from the database. Short values favour
	// prompt permission-revoke propagation; long values favour DB load.
	PermissionTTL time.Duration
}

// Environments ENV may name. Only production changes behaviour (JSON
// logs, gin release mode, `make db-reset` refused); anything else logs at
// debug level, with the emailed codes when SMTP is off.
var environments = []string{"development", "staging", "production"}

// sslModes are the DB_SSLMODE values both the API (pgx) and the migrate
// CLI (lib/pq) accept: prefer and allow break the migrations.
var sslModes = []string{"disable", "require", "verify-ca", "verify-full"}

// Load reads the configuration from the environment. A value that is set
// but cannot be read — "yes" for a boolean, "30" for a duration, an ENV
// spelt "prod" — is an error, never the default in disguise: that could
// quietly switch off a protection or point two environments at one cache.
func Load() (*Config, error) {
	var l loader
	cfg := &Config{
		Database: DatabaseConfig{
			Host:              getEnv("DB_HOST", "localhost"),
			Port:              getEnv("DB_PORT", "5432"),
			User:              getEnv("DB_USER", "postgres"),
			Password:          getEnv("DB_PASSWORD", "postgres"),
			DBName:            getEnv("DB_NAME", "timebox"),
			SSLMode:           getEnv("DB_SSLMODE", "disable"),
			MaxConns:          l.int("DB_MAX_CONNS", 25),
			MinConns:          l.int("DB_MIN_CONNS", 5),
			MaxConnLifetime:   l.duration("DB_MAX_CONN_LIFETIME", time.Hour),
			MaxConnIdleTime:   l.duration("DB_MAX_CONN_IDLE_TIME", 30*time.Minute),
			HealthCheckPeriod: l.duration("DB_HEALTH_CHECK_PERIOD", time.Minute),
			ConnectTimeout:    l.duration("DB_CONNECT_TIMEOUT", 5*time.Second),
		},
		Server: ServerConfig{
			Port:               getEnv("SERVER_PORT", "8080"),
			Env:                getEnv("ENV", "development"),
			TrustedProxies:     getEnvList("TRUSTED_PROXIES"),
			CORSAllowedOrigins: getEnvList("CORS_ALLOWED_ORIGINS"),
		},
		RateLimit: RateLimitConfig{
			AuthLimit:  l.int("AUTH_RATE_LIMIT", 20),
			AuthWindow: l.duration("AUTH_RATE_LIMIT_WINDOW", time.Minute),
		},
		Security: SecurityConfig{
			// true needs SMTP: without it nobody can verify, and the API
			// refuses to start.
			EmailVerificationRequired: l.bool("EMAIL_VERIFICATION_REQUIRED", false),
			MaxLoginAttempts:          l.int("MAX_LOGIN_ATTEMPTS", 5),
			AccountLockoutDuration:    l.duration("ACCOUNT_LOCKOUT_DURATION", 30*time.Minute),
			RefreshTokenExpiration:    l.duration("JWT_REFRESH_EXPIRATION", 7*24*time.Hour),
			OTPExpiration:             l.duration("OTP_EXPIRATION", 15*time.Minute),
			OTPMaxAttempts:            l.int("OTP_MAX_ATTEMPTS", 5),
			OTPResendCooldown:         l.duration("OTP_RESEND_COOLDOWN", time.Minute),
			ResetTokenExpiration:      l.duration("RESET_TOKEN_EXPIRATION", 10*time.Minute),
			MaxVerificationEmails:     l.int("MAX_VERIFICATION_EMAILS", 5),
			MaxPasswordResetEmails:    l.int("MAX_PASSWORD_RESET_EMAILS", 3),
		},
		Redis: RedisConfig{
			Host:          getEnv("REDIS_HOST", "localhost"),
			Port:          getEnv("REDIS_PORT", "6379"),
			Password:      getEnv("REDIS_PASSWORD", ""),
			DB:            l.int("REDIS_DB", 0),
			PermissionTTL: l.duration("REDIS_PERMISSION_TTL", 10*time.Minute),
		},
		Google: googleauth.Config{
			Desktop: googleauth.Client{
				ID:     getEnv("GOOGLE_DESKTOP_CLIENT_ID", ""),
				Secret: getEnv("GOOGLE_DESKTOP_CLIENT_SECRET", ""),
			},
			Web: googleauth.Client{
				ID:     getEnv("GOOGLE_WEB_CLIENT_ID", ""),
				Secret: getEnv("GOOGLE_WEB_CLIENT_SECRET", ""),
			},
			WebRedirectURIs: getEnvList("GOOGLE_WEB_REDIRECT_URIS"),
		},
		Email: email.Config{
			Host:      getEnv("SMTP_HOST", ""),
			Port:      l.int("SMTP_PORT", 587),
			Username:  getEnv("SMTP_USER", ""),
			Password:  getEnv("SMTP_PASSWORD", ""),
			FromEmail: getEnv("SMTP_FROM_EMAIL", ""),
			// Empty follows SMTP_FROM_EMAIL.
			NotificationsFromEmail: getEnv("SMTP_NOTIFICATIONS_FROM_EMAIL", ""),
			// Empty follows AppName.
			FromName:     getEnv("SMTP_FROM_NAME", ""),
			AppName:      getEnv("APP_NAME", email.DefaultAppName),
			FrontendURL:  getEnv("FRONTEND_URL", "https://app.timebox.id"),
			SupportEmail: getEnv("SUPPORT_EMAIL", ""),
		},
		Storage: storage.Config{
			AccountID:       getEnv("R2_ACCOUNT_ID", ""),
			AccessKeyID:     getEnv("R2_ACCESS_KEY_ID", ""),
			SecretAccessKey: getEnv("R2_SECRET_ACCESS_KEY", ""),
			Bucket:          getEnv("R2_BUCKET", ""),
			PublicURL:       getEnv("R2_PUBLIC_URL", ""),
		},
		Audit: audit.Config{
			URI:       getEnv("MONGO_URI", ""),
			Database:  getEnv("MONGO_DATABASE", audit.DefaultDatabase),
			Retention: l.duration("AUDIT_LOG_RETENTION", audit.DefaultRetention),
		},
	}
	// Read by pkg/jwt on its own; checked here so a typo stops the start.
	l.duration("JWT_EXPIRATION", 0)
	l.oneOf("ENV", cfg.Server.Env, environments...)
	l.oneOf("DB_SSLMODE", cfg.Database.SSLMode, sslModes...)
	if cfg.Security.OTPMaxAttempts < 1 {
		l.fail("OTP_MAX_ATTEMPTS", strconv.Itoa(cfg.Security.OTPMaxAttempts), "at least 1")
	}
	if err := cfg.Google.Validate(); err != nil {
		l.errs = append(l.errs, err)
	}

	if err := errors.Join(l.errs...); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

// GetDSN builds the connection URL. Every part is escaped, so a password
// containing characters such as @ : / # ? % still connects.
func (c *DatabaseConfig) GetDSN() string {
	query := url.Values{}
	query.Set("sslmode", c.SSLMode)
	query.Set("timezone", "UTC")

	dsn := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User, c.Password),
		Host:     net.JoinHostPort(c.Host, c.Port),
		Path:     "/" + c.DBName,
		RawQuery: query.Encode(),
	}
	return dsn.String()
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// Numeric, boolean and duration values are trimmed before parsing. `make`
// loads .env with `include`, which keeps the spaces in front of an inline
// comment as part of the value; untrimmed, "10   # comment" would fail to
// parse.

// getEnvList splits a comma-separated variable, dropping blanks.
func getEnvList(key string) []string {
	var out []string
	for _, item := range strings.Split(os.Getenv(key), ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// loader reads the typed variables and collects every value it could not
// read, so one start reports them all.
type loader struct {
	errs []error
}

func (l *loader) fail(key, value, want string) {
	l.errs = append(l.errs, fmt.Errorf("%s=%q is not %s", key, value, want))
}

func (l *loader) bool(key string, defaultValue bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		l.fail(key, value, "true or false")
		return defaultValue
	}
	return b
}

func (l *loader) int(key string, defaultValue int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		l.fail(key, value, "a whole number")
		return defaultValue
	}
	return n
}

func (l *loader) duration(key string, defaultValue time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		l.fail(key, value, "a duration with a unit, e.g. 30s, 15m, 168h")
		return defaultValue
	}
	return d
}

// oneOf checks that value, read from key, is one of allowed.
func (l *loader) oneOf(key, value string, allowed ...string) {
	if !slices.Contains(allowed, value) {
		l.fail(key, value, "one of "+strings.Join(allowed, ", "))
	}
}
