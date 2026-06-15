package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Database     DatabaseConfig
	Server       ServerConfig
	Security     SecurityConfig
	Auth         AuthConfig
	WAHA         WAHAConfig
	OpenAI       OpenAIConfig
	Redis        RedisConfig
	GCS          GCSConfig
	Firebase     FirebaseConfig
	RabbitMQ     RabbitMQConfig
	Sentry       SentryConfig
	Cache        CacheConfig
	Storage      StorageConfig
	Notification NotificationConfig
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

type AuthConfig struct {
	// DefaultAdminRoleID is the role assigned to a newly registered user
	// in their freshly created company. Maps to core.roles.id.
	DefaultAdminRoleID string
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

// RabbitMQConfig configures the shared AMQP client (internal/shared/rabbitmq).
// This is the one new infra subsystem consumed directly as config.RabbitMQConfig
// (it lives under internal/shared/ and is allowed to import internal/config,
// mirroring RedisConfig). The other four subsystems below are env holders that
// get mapped to package-local config structs in main.go.
type RabbitMQConfig struct {
	// Enabled gates the whole subsystem. When false, the app skips connecting
	// to RabbitMQ entirely and boots without messaging (degraded). Defaults to
	// true. Even when true, a failed connection is non-fatal — see router.Setup.
	Enabled           bool
	URL               string // amqp://user:pass@host:port/vhost (full override, optional)
	Host              string
	Port              string
	User              string
	Password          string
	VHost             string
	Exchange          string        // default topic exchange owned by the app
	ReconnectInterval time.Duration // retry delay when the connection drops
	PrefetchCount     int           // consumer QoS
	PublishTimeout    time.Duration // how long to wait for a publisher confirm
}

// SentryConfig holds Sentry env values. Mapped to pkg/sentry.Config in main.go.
type SentryConfig struct {
	DSN              string
	Environment      string
	Release          string
	TracesSampleRate float64
}

// CacheConfig holds cache env values. Mapped to pkg/cache.Config in main.go.
// The cache reuses the existing Redis client, so connection details live in
// RedisConfig — only namespacing/TTL belong here.
type CacheConfig struct {
	KeyPrefix  string
	DefaultTTL time.Duration
}

// StorageConfig holds storage env values. Mapped to pkg/storage.Config in
// main.go. GCSConfig is kept separately for backward compatibility; this
// struct carries the provider selector plus S3/MinIO settings.
type StorageConfig struct {
	Provider          string // gcs|s3|minio
	S3Endpoint        string
	S3Region          string
	S3Bucket          string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3PublicBaseURL   string
	S3UsePathStyle    bool
}

// NotificationConfig holds notification env values. Mapped to
// pkg/notification.Config in main.go.
type NotificationConfig struct {
	TwilioAccountSID   string
	TwilioAuthToken    string
	TwilioFromNumber   string
	FCMCredentialsJSON string
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
		Auth: AuthConfig{
			DefaultAdminRoleID: getEnv("AUTH_DEFAULT_ADMIN_ROLE_ID", "00000000-0000-0000-0000-000000000002"),
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
		RabbitMQ: RabbitMQConfig{
			Enabled:           getEnvBool("RABBITMQ_ENABLED", true),
			URL:               getEnv("RABBITMQ_URL", ""),
			Host:              getEnv("RABBITMQ_HOST", "localhost"),
			Port:              getEnv("RABBITMQ_PORT", "5672"),
			User:              getEnv("RABBITMQ_USER", "guest"),
			Password:          getEnv("RABBITMQ_PASSWORD", "guest"),
			VHost:             getEnv("RABBITMQ_VHOST", "/"),
			Exchange:          getEnv("RABBITMQ_EXCHANGE", "skeleton.events"),
			ReconnectInterval: getEnvDuration("RABBITMQ_RECONNECT_INTERVAL", 5*time.Second),
			PrefetchCount:     getEnvInt("RABBITMQ_PREFETCH_COUNT", 10),
			PublishTimeout:    getEnvDuration("RABBITMQ_PUBLISH_TIMEOUT", 5*time.Second),
		},
		Sentry: SentryConfig{
			DSN:              getEnv("SENTRY_DSN", ""),
			Environment:      getEnv("SENTRY_ENVIRONMENT", getEnv("ENV", "development")),
			Release:          getEnv("SENTRY_RELEASE", ""),
			TracesSampleRate: getEnvFloat64("SENTRY_TRACES_SAMPLE_RATE", 0.0),
		},
		Cache: CacheConfig{
			KeyPrefix:  getEnv("CACHE_KEY_PREFIX", "cache"),
			DefaultTTL: getEnvDuration("CACHE_DEFAULT_TTL", 5*time.Minute),
		},
		Storage: StorageConfig{
			Provider:          getEnv("STORAGE_PROVIDER", "gcs"),
			S3Endpoint:        getEnv("S3_ENDPOINT", ""),
			S3Region:          getEnv("S3_REGION", "us-east-1"),
			S3Bucket:          getEnv("S3_BUCKET", ""),
			S3AccessKeyID:     getEnv("S3_ACCESS_KEY_ID", ""),
			S3SecretAccessKey: getEnv("S3_SECRET_ACCESS_KEY", ""),
			S3PublicBaseURL:   getEnv("S3_PUBLIC_BASE_URL", ""),
			S3UsePathStyle:    getEnvBool("S3_USE_PATH_STYLE", false),
		},
		Notification: NotificationConfig{
			TwilioAccountSID:   getEnv("TWILIO_ACCOUNT_SID", ""),
			TwilioAuthToken:    getEnv("TWILIO_AUTH_TOKEN", ""),
			TwilioFromNumber:   getEnv("TWILIO_FROM_NUMBER", ""),
			FCMCredentialsJSON: getEnv("FCM_CREDENTIALS_JSON", ""),
		},
	}
}

// GetURL returns the AMQP connection URL. When URL is set it wins; otherwise
// the URL is assembled from the discrete components. Mirrors
// DatabaseConfig.GetDSN().
func (c *RabbitMQConfig) GetURL() string {
	if c.URL != "" {
		return c.URL
	}
	return fmt.Sprintf("amqp://%s:%s@%s:%s%s", c.User, c.Password, c.Host, c.Port, c.VHost)
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

func getEnvFloat64(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		floatValue, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return defaultValue
		}
		return floatValue
	}
	return defaultValue
}
