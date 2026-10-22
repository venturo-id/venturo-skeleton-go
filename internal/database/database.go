package database

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"venturo-skeleton-go-simple/pkg/logger"
)

type Database struct {
	Pool *pgxpool.Pool
}

// ParseConfig creates a pgxpool.Config from DSN and environment variables
func ParseConfig(dsn string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DSN: %w", err)
	}

	// Configure MaxConns - Maximum number of connections in the pool
	if maxConns := getEnvAsInt("DB_MAX_CONNS", 25); maxConns > 0 {
		config.MaxConns = int32(maxConns)
	}

	// Configure MinConns - Minimum number of idle connections
	if minConns := getEnvAsInt("DB_MIN_CONNS", 5); minConns > 0 {
		config.MinConns = int32(minConns)
	}

	// Configure MaxConnLifetime - Maximum lifetime of a connection
	if lifetime := getEnvAsDuration("DB_MAX_CONN_LIFETIME", "1h"); lifetime > 0 {
		config.MaxConnLifetime = lifetime
	}

	// Configure MaxConnIdleTime - Maximum idle time before closing
	if idleTime := getEnvAsDuration("DB_MAX_CONN_IDLE_TIME", "30m"); idleTime > 0 {
		config.MaxConnIdleTime = idleTime
	}

	// Configure HealthCheckPeriod - Interval for connection health checks
	if healthCheck := getEnvAsDuration("DB_HEALTH_CHECK_PERIOD", "1m"); healthCheck > 0 {
		config.HealthCheckPeriod = healthCheck
	}

	// Configure ConnConfig - Connection-level settings
	if connectTimeout := getEnvAsDuration("DB_CONNECT_TIMEOUT", "5s"); connectTimeout > 0 {
		config.ConnConfig.ConnectTimeout = connectTimeout
	}

	return config, nil
}

func New(dsn string) (*Database, error) {
	// Parse config with custom pool settings
	config, err := ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	// Create connection pool with config
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Log pool stats
	logPoolStats(pool)

	return &Database{Pool: pool}, nil
}

func (d *Database) Close() {
	d.Pool.Close()
}

// Helper functions

func getEnvAsInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultVal
}

func getEnvAsDuration(key string, defaultVal string) time.Duration {
	val := os.Getenv(key)
	if val == "" {
		val = defaultVal
	}
	duration, err := time.ParseDuration(val)
	if err != nil {
		// Fallback to default if parsing fails
		duration, _ = time.ParseDuration(defaultVal)
	}
	return duration
}

func logPoolStats(pool *pgxpool.Pool) {
	stat := pool.Stat()
	logger.Info("Database connection pool initialized",
		logger.Int32("max_conns", stat.MaxConns()),
		logger.Int32("total_conns", stat.TotalConns()),
		logger.Int32("idle_conns", stat.IdleConns()),
		logger.Int32("acquired_conns", stat.AcquiredConns()),
	)
}