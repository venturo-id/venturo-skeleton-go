// Package cache is a small typed cache over Redis. It follows the repo's pkg/
// convention: a package-local Config and an injected *goredis.Client (the same
// instance built by internal/shared/redis — never a new connection), so it
// never imports internal/config.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Config holds cache namespacing and default TTL. Mirrors pkg/email.SMTPConfig:
// a local struct the caller populates (main.go maps internal/config values).
type Config struct {
	KeyPrefix  string
	DefaultTTL time.Duration
}

// Cache is the abstraction over a key/value cache with JSON values.
type Cache interface {
	// Get unmarshals the value for key into dest. Returns (false, nil) on a
	// cache miss.
	Get(ctx context.Context, key string, dest any) (bool, error)
	// Set JSON-marshals val and stores it under key. ttl <= 0 uses DefaultTTL.
	Set(ctx context.Context, key string, val any, ttl time.Duration) error
	// Delete removes one or more keys.
	Delete(ctx context.Context, keys ...string) error
	// GetOrSet returns the cached value for key, or computes it via loader,
	// stores it, and returns it. The computed value is written into dest.
	GetOrSet(ctx context.Context, key string, dest any, ttl time.Duration, loader func() (any, error)) error
}

// RedisCache implements Cache on top of go-redis.
type RedisCache struct {
	client *goredis.Client
	cfg    Config
}

// New builds a RedisCache. The client is shared with the rest of the app — do
// not open a new connection here.
func New(client *goredis.Client, cfg Config) *RedisCache {
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "cache"
	}
	if cfg.DefaultTTL <= 0 {
		cfg.DefaultTTL = 5 * time.Minute
	}
	return &RedisCache{client: client, cfg: cfg}
}

// namespaced applies the configured key prefix.
func (c *RedisCache) namespaced(key string) string {
	return c.cfg.KeyPrefix + ":" + key
}

func (c *RedisCache) Get(ctx context.Context, key string, dest any) (bool, error) {
	raw, err := c.client.Get(ctx, c.namespaced(key)).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return false, nil
		}
		return false, fmt.Errorf("cache get %q: %w", key, err)
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, fmt.Errorf("cache unmarshal %q: %w", key, err)
	}
	return true, nil
}

func (c *RedisCache) Set(ctx context.Context, key string, val any, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = c.cfg.DefaultTTL
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("cache marshal %q: %w", key, err)
	}
	if err := c.client.Set(ctx, c.namespaced(key), raw, ttl).Err(); err != nil {
		return fmt.Errorf("cache set %q: %w", key, err)
	}
	return nil
}

func (c *RedisCache) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	prefixed := make([]string, len(keys))
	for i, k := range keys {
		prefixed[i] = c.namespaced(k)
	}
	if err := c.client.Del(ctx, prefixed...).Err(); err != nil {
		return fmt.Errorf("cache delete: %w", err)
	}
	return nil
}

func (c *RedisCache) GetOrSet(ctx context.Context, key string, dest any, ttl time.Duration, loader func() (any, error)) error {
	found, err := c.Get(ctx, key, dest)
	if err != nil {
		return err
	}
	if found {
		return nil
	}

	val, err := loader()
	if err != nil {
		return fmt.Errorf("cache loader %q: %w", key, err)
	}
	if err := c.Set(ctx, key, val, ttl); err != nil {
		return err
	}

	// Round-trip the loaded value into dest so callers get a consistent
	// (post-JSON) representation regardless of hit/miss.
	raw, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("cache marshal loaded %q: %w", key, err)
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("cache unmarshal loaded %q: %w", key, err)
	}
	return nil
}

// GetJSON is an ergonomic generic helper over Cache.Get.
func GetJSON[T any](ctx context.Context, c Cache, key string) (T, bool, error) {
	var out T
	found, err := c.Get(ctx, key, &out)
	return out, found, err
}
