package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

type sample struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func newTestCache(t *testing.T) (*RedisCache, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	return New(client, Config{KeyPrefix: "test", DefaultTTL: time.Minute}), mr
}

func TestSetGetDeleteRoundTrip(t *testing.T) {
	c, mr := newTestCache(t)
	defer mr.Close()
	ctx := context.Background()

	want := sample{ID: 1, Name: "alice"}
	if err := c.Set(ctx, "user:1", want, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	var got sample
	found, err := c.Get(ctx, "user:1", &got)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatal("expected cache hit, got miss")
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	if err := c.Delete(ctx, "user:1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	found, err = c.Get(ctx, "user:1", &got)
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if found {
		t.Fatal("expected cache miss after delete")
	}
}

func TestGetMissReturnsFalse(t *testing.T) {
	c, mr := newTestCache(t)
	defer mr.Close()

	var got sample
	found, err := c.Get(context.Background(), "missing", &got)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found {
		t.Fatal("expected miss for absent key")
	}
}

func TestGetOrSetComputesThenCaches(t *testing.T) {
	c, mr := newTestCache(t)
	defer mr.Close()
	ctx := context.Background()

	calls := 0
	loader := func() (any, error) {
		calls++
		return sample{ID: 2, Name: "bob"}, nil
	}

	var first sample
	if err := c.GetOrSet(ctx, "user:2", &first, 0, loader); err != nil {
		t.Fatalf("GetOrSet (miss): %v", err)
	}
	if first.Name != "bob" {
		t.Fatalf("got %+v", first)
	}

	var second sample
	if err := c.GetOrSet(ctx, "user:2", &second, 0, loader); err != nil {
		t.Fatalf("GetOrSet (hit): %v", err)
	}
	if calls != 1 {
		t.Fatalf("loader called %d times, want 1 (second call should hit cache)", calls)
	}
	if second != first {
		t.Fatalf("hit value %+v != miss value %+v", second, first)
	}
}

func TestGetJSONGeneric(t *testing.T) {
	c, mr := newTestCache(t)
	defer mr.Close()
	ctx := context.Background()

	if err := c.Set(ctx, "k", sample{ID: 9, Name: "carol"}, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, found, err := GetJSON[sample](ctx, c, "k")
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if !found || got.Name != "carol" {
		t.Fatalf("got %+v found=%v", got, found)
	}
}
