// Package redis owns the shared Redis client used by the rest of the app.
//
// We keep this tiny on purpose: just a *redis.Client builder. Feature
// packages (e.g. authz) depend on *redis.Client directly so they can use
// the full go-redis API without any thin wrapper to maintain.
package redis

import (
	"context"
	"fmt"
	"net"
	"time"

	"timebox-be/internal/config"

	goredis "github.com/redis/go-redis/v9"
)

// KeyPrefix starts every key this application writes, so it can share a
// Redis server or database with other applications without key clashes —
// and without one application's SCAN-and-delete touching the other's keys.
const KeyPrefix = "timebox:"

// New builds a Redis client from config and verifies connectivity with PING.
// Returns an error so the caller can decide whether Redis is load-bearing
// (fatal) or optional (degraded-mode fallback).
func New(ctx context.Context, cfg config.RedisConfig) (*goredis.Client, error) {
	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	client := goredis.NewClient(&goredis.Options{
		Addr:         addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		PoolSize:     20,
		MinIdleConns: 2,
	})

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis ping failed (%s db=%d): %w", addr, cfg.DB, err)
	}

	return client, nil
}
