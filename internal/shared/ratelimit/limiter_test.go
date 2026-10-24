package ratelimit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// newTestLimiter connects to the Redis in TEST_REDIS_ADDR (default
// localhost:6379, DB 15) and skips when none is reachable.
func newTestLimiter(t *testing.T) *Limiter {
	t.Helper()

	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := goredis.NewClient(&goredis.Options{Addr: addr, DB: 15})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("redis not reachable at %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return New(client)
}

func TestLimiter_Allow(t *testing.T) {
	l := newTestLimiter(t)
	fixed := time.Date(2026, 1, 1, 10, 0, 20, 0, time.UTC)
	l.now = func() time.Time { return fixed }

	ctx := context.Background()
	key := "test:" + uuid.NewString()

	for i := 1; i <= 3; i++ {
		allowed, _, err := l.Allow(ctx, key, 3, time.Minute)
		if err != nil || !allowed {
			t.Fatalf("hit %d: allowed=%v err=%v, want allowed", i, allowed, err)
		}
	}

	allowed, retryAfter, err := l.Allow(ctx, key, 3, time.Minute)
	if err != nil || allowed {
		t.Fatalf("hit 4: allowed=%v err=%v, want refused", allowed, err)
	}
	if retryAfter != 40*time.Second {
		t.Errorf("retryAfter = %s, want 40s (rest of the minute window)", retryAfter)
	}

	// The next window starts a fresh count.
	l.now = func() time.Time { return fixed.Add(time.Minute) }
	if allowed, _, err := l.Allow(ctx, key, 3, time.Minute); err != nil || !allowed {
		t.Fatalf("next window: allowed=%v err=%v, want allowed", allowed, err)
	}
}

func TestLimiter_AllowWithoutLimit(t *testing.T) {
	l := New(nil) // never touches Redis when the limit is disabled

	if allowed, _, err := l.Allow(context.Background(), "k", 0, time.Minute); err != nil || !allowed {
		t.Fatalf("limit 0: allowed=%v err=%v, want allowed", allowed, err)
	}
}

func TestLimiter_Hold(t *testing.T) {
	l := newTestLimiter(t)
	ctx := context.Background()
	key := "test:" + uuid.NewString()

	held, _, err := l.Hold(ctx, key, time.Minute)
	if err != nil || !held {
		t.Fatalf("first hold: held=%v err=%v, want held", held, err)
	}

	held, remaining, err := l.Hold(ctx, key, time.Minute)
	if err != nil || held {
		t.Fatalf("second hold: held=%v err=%v, want refused", held, err)
	}
	if remaining <= 50*time.Second || remaining > time.Minute {
		t.Errorf("remaining = %s, want just under a minute", remaining)
	}

	if left, err := l.Remaining(ctx, "test:"+uuid.NewString()); err != nil || left != 0 {
		t.Errorf("Remaining of an unknown key = %s, %v, want 0", left, err)
	}
}
