package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Database DatabaseConfig
	Server   ServerConfig
	Security SecurityConfig
	WAHA     WAHAConfig
	OpenAI   OpenAIConfig
	Redis    RedisConfig
	GCS      GCSConfig
	Firebase FirebaseConfig
}

type GCSConfig struct {
	BucketName      string
	ProjectID       string
	CredentialsJSON string
}

// FirebaseConfig holds credentials for the Firebase Admin SDK used to
// verify ID tokens coming from FE Google sign-in (and any future
// Firebase-backed providers).
//
// ProjectID is mandatory — VerifyIDToken refuses to validate without it.
// CredentialsJSON is the raw JSON content of a service-account key.
// Leave it empty in environments where Application Default Credentials
// are available (e.g. GKE workload identity) — the SDK will pick them
// up automatically.
type FirebaseConfig struct {
	ProjectID       string
	CredentialsJSON string
}

type DatabaseConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
}

type ServerConfig struct {
	Port string
	Env  string
}

type SecurityConfig struct {
	EmailVerificationRequired bool
	MaxLoginAttempts          int
	AccountLockoutDuration    time.Duration
}

type WAHAConfig struct {
	BaseURL       string
	APIKey        string
	WebhookURL    string
	WebhookSecret string
	HTTPTimeout   int // HTTP timeout in seconds
}

type OpenAIConfig struct {
	APIKey  string
	Model   string
	Timeout int // API timeout in seconds
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

func Load() *Config {
	return &Config{
		Database: DatabaseConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnv("DB_PORT", "5432"),
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", "postgres"),
			DBName:   getEnv("DB_NAME", "tuai"),
			SSLMode:  getEnv("DB_SSLMODE", "disable"),
		},
		Server: ServerConfig{
			Port: getEnv("SERVER_PORT", "8080"),
			Env:  getEnv("ENV", "development"),
		},
		Security: SecurityConfig{
			EmailVerificationRequired: getEnvBool("EMAIL_VERIFICATION_REQUIRED", true),
			MaxLoginAttempts:          getEnvInt("MAX_LOGIN_ATTEMPTS", 5),
			AccountLockoutDuration:    getEnvDuration("ACCOUNT_LOCKOUT_DURATION", 30*time.Minute),
		},
		WAHA: WAHAConfig{
			BaseURL:       getEnv("WAHA_BASE_URL", "https://wapi.venturo.id"),
			APIKey:        getEnv("WAHA_API_KEY", ""),
			WebhookURL:    getEnv("WAHA_WEBHOOK_URL", ""),
			WebhookSecret: getEnv("WAHA_WEBHOOK_SECRET", ""),
			HTTPTimeout:   getEnvInt("WAHA_HTTP_TIMEOUT", 30),
		},
		OpenAI: OpenAIConfig{
			APIKey:  getEnv("OPENAI_API_KEY", ""),
			Model:   getEnv("OPENAI_MODEL", "gpt-4o-mini"),
			Timeout: getEnvInt("OPENAI_TIMEOUT", 120),
		},
		Redis: RedisConfig{
			Host:          getEnv("REDIS_HOST", "localhost"),
			Port:          getEnv("REDIS_PORT", "6379"),
			Password:      getEnv("REDIS_PASSWORD", ""),
			DB:            getEnvInt("REDIS_DB", 10),
			PermissionTTL: getEnvDuration("REDIS_PERMISSION_TTL", 10*time.Minute),
		},
		GCS: GCSConfig{
			BucketName:      getEnv("GCS_BUCKET_NAME", ""),
			ProjectID:       getEnv("GCS_PROJECT_ID", ""),
			CredentialsJSON: getEnv("GCS_CREDENTIALS_JSON", ""),
		},
		Firebase: FirebaseConfig{
			ProjectID:       getEnv("FIREBASE_PROJECT_ID", ""),
			CredentialsJSON: getEnv("FIREBASE_CREDENTIALS_JSON", ""),
		},
	}
}

func (c *DatabaseConfig) GetDSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s&timezone=UTC",
		c.User, c.Password, c.Host, c.Port, c.DBName, c.SSLMode,
	)
}

func (c *DatabaseConfig) GetMigrationURL() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s&timezone=UTC",
		c.User, c.Password, c.Host, c.Port, c.DBName, c.SSLMode,
	)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		boolValue, err := strconv.ParseBool(value)
		if err != nil {
			return defaultValue
		}
		return boolValue
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		intValue, err := strconv.Atoi(value)
		if err != nil {
			return defaultValue
		}
		return intValue
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil {
			return defaultValue
		}
		return duration
	}
	return defaultValue
}