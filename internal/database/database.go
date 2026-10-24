package database

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"timebox-be/internal/config"
	"timebox-be/pkg/logger"
)

// Session settings of every connection, sent at connect time.
const (
	// applicationName names the connections in pg_stat_activity.
	applicationName = "timebox-api"
	// statementTimeout ends any statement that runs longer. Requests run
	// in well under a second, the hourly token cleanup in a few, and
	// migrations go through the migrate CLI, not this pool.
	statementTimeout = 30 * time.Second
	// idleInTransactionTimeout ends a session left idle inside an open
	// transaction (a stuck request), releasing the locks it holds.
	idleInTransactionTimeout = 60 * time.Second
)

type Database struct {
	Pool *pgxpool.Pool
}

// ParseConfig turns the database settings into a pgxpool.Config. Zero
// pool settings keep pgx's own defaults.
func ParseConfig(cfg config.DatabaseConfig) (*pgxpool.Config, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.GetDSN())
	if err != nil {
		return nil, fmt.Errorf("failed to parse DSN: %w", err)
	}

	params := poolConfig.ConnConfig.RuntimeParams
	params["application_name"] = applicationName
	params["statement_timeout"] = milliseconds(statementTimeout)
	params["idle_in_transaction_session_timeout"] = milliseconds(idleInTransactionTimeout)

	if cfg.MaxConns > 0 {
		poolConfig.MaxConns = int32(cfg.MaxConns)
	}
	if cfg.MinConns > 0 {
		poolConfig.MinConns = int32(cfg.MinConns)
	}
	if cfg.MaxConnLifetime > 0 {
		poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.HealthCheckPeriod > 0 {
		poolConfig.HealthCheckPeriod = cfg.HealthCheckPeriod
	}
	if cfg.ConnectTimeout > 0 {
		poolConfig.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	}

	return poolConfig, nil
}

// New opens the connection pool and verifies it with a ping.
func New(ctx context.Context, cfg config.DatabaseConfig) (*Database, error) {
	poolConfig, err := ParseConfig(cfg)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	logPoolStats(pool)

	return &Database{Pool: pool}, nil
}

// milliseconds writes d the way PostgreSQL reads a timeout without a unit
func milliseconds(d time.Duration) string {
	return strconv.FormatInt(d.Milliseconds(), 10)
}

func (d *Database) Close() {
	d.Pool.Close()
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
