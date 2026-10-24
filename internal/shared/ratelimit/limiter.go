// Package ratelimit is a fixed-window request counter backed by Redis, so
// a limit holds across every API replica rather than per pod.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	sharedRedis "timebox-be/internal/shared/redis"

	goredis "github.com/redis/go-redis/v9"
)

// ExceededError is returned when a caller has used up its window. Callers
// map it to HTTP 429 with a Retry-After header.
type ExceededError struct {
	RetryAfter time.Duration
}

func (e *ExceededError) Error() string {
	return fmt.Sprintf("rate limit exceeded, retry after %s", e.RetryAfter)
}

// Limiter counts hits per key in fixed windows.
type Limiter struct {
	redis *goredis.Client
	now   func() time.Time
}

// New builds a Limiter on the shared Redis client.
func New(redis *goredis.Client) *Limiter {
	return &Limiter{redis: redis, now: time.Now}
}

// Allow records one hit for key and reports whether it is within limit
// hits per window. When it is not, retryAfter is the time left in the
// current window.
//
// On a Redis error Allow returns allowed=true together with the error:
// losing the rate limit for a moment is better than refusing every
// request, and the caller decides whether to log it.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error) {
	if limit <= 0 || window <= 0 {
		return true, 0, nil
	}

	now := l.now()
	windowStart := now.Truncate(window)
	redisKey := fmt.Sprintf("%sratelimit:%s:%d", sharedRedis.KeyPrefix, key, windowStart.Unix())

	pipe := l.redis.TxPipeline()
	incr := pipe.Incr(ctx, redisKey)
	// The key is unique to this window, so re-arming the TTL on every hit
	// only keeps it alive until the window closes.
	pipe.Expire(ctx, redisKey, window)
	if _, err := pipe.Exec(ctx); err != nil {
		return true, 0, fmt.Errorf("ratelimit: redis: %w", err)
	}

	if incr.Val() > int64(limit) {
		return false, windowStart.Add(window).Sub(now), nil
	}
	return true, 0, nil
}

// Hold starts a cooldown of d on key unless one is running: it reports
// whether it started one and, when not, the time left on the running one.
// Unlike Allow's windows, a cooldown counts from the moment it starts.
//
// On a Redis error Hold returns held=true together with the error, like
// Allow: the caller goes ahead and decides whether to log it.
func (l *Limiter) Hold(ctx context.Context, key string, d time.Duration) (held bool, remaining time.Duration, err error) {
	if d <= 0 {
		return true, 0, nil
	}

	redisKey := sharedRedis.KeyPrefix + "cooldown:" + key
	set, err := l.redis.SetNX(ctx, redisKey, 1, d).Result()
	if err != nil {
		return true, 0, fmt.Errorf("ratelimit: redis: %w", err)
	}
	if set {
		return true, 0, nil
	}
	remaining, err = l.Remaining(ctx, key)
	return false, remaining, err
}

// Remaining returns the time left on key's cooldown; 0 when none runs.
func (l *Limiter) Remaining(ctx context.Context, key string) (time.Duration, error) {
	ttl, err := l.redis.PTTL(ctx, sharedRedis.KeyPrefix+"cooldown:"+key).Result()
	if err != nil {
		return 0, fmt.Errorf("ratelimit: redis: %w", err)
	}
	if ttl < 0 {
		// -2: no key; -1: no expiry, which Hold never leaves.
		return 0, nil
	}
	return ttl, nil
}
