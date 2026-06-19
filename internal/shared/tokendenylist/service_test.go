package tokendenylist

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func newTestService(t *testing.T) (*Service, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	return NewService(client), mr
}

func TestDenyJTIRoundTrip(t *testing.T) {
	s, mr := newTestService(t)
	defer mr.Close()
	ctx := context.Background()

	if s.IsJTIDenied(ctx, "jti-1") {
		t.Fatal("jti should not be denied before DenyJTI")
	}
	if err := s.DenyJTI(ctx, "jti-1", time.Minute); err != nil {
		t.Fatalf("DenyJTI: %v", err)
	}
	if !s.IsJTIDenied(ctx, "jti-1") {
		t.Fatal("jti should be denied after DenyJTI")
	}
}

func TestDenyJTITTLExpires(t *testing.T) {
	s, mr := newTestService(t)
	defer mr.Close()
	ctx := context.Background()

	if err := s.DenyJTI(ctx, "jti-exp", time.Minute); err != nil {
		t.Fatalf("DenyJTI: %v", err)
	}
	// miniredis doesn't advance TTLs on its own — fast-forward past it.
	mr.FastForward(2 * time.Minute)
	if s.IsJTIDenied(ctx, "jti-exp") {
		t.Fatal("jti should auto-expire after its TTL")
	}
}

func TestDenyJTINoopOnBadInput(t *testing.T) {
	s, mr := newTestService(t)
	defer mr.Close()
	ctx := context.Background()

	if err := s.DenyJTI(ctx, "", time.Minute); err != nil {
		t.Fatalf("empty jti should be a no-op, got %v", err)
	}
	if err := s.DenyJTI(ctx, "jti", 0); err != nil {
		t.Fatalf("non-positive ttl should be a no-op, got %v", err)
	}
	if s.IsJTIDenied(ctx, "jti") {
		t.Fatal("nothing should have been written")
	}
}

func TestIsIssuedBeforeCutoff(t *testing.T) {
	s, mr := newTestService(t)
	defer mr.Close()
	ctx := context.Background()

	cutoff := time.Now()
	if err := s.DenyUserBefore(ctx, "user-1", cutoff, time.Hour); err != nil {
		t.Fatalf("DenyUserBefore: %v", err)
	}

	// Token issued before the cutoff → revoked.
	before := cutoff.Add(-time.Minute)
	if !s.IsIssuedBeforeCutoff(ctx, "user-1", before) {
		t.Fatal("token issued before cutoff should be revoked")
	}
	// Token issued after the cutoff (a fresh login) → still valid.
	after := cutoff.Add(time.Minute)
	if s.IsIssuedBeforeCutoff(ctx, "user-1", after) {
		t.Fatal("token issued after cutoff should remain valid")
	}
	// A user with no cutoff stamped → never revoked.
	if s.IsIssuedBeforeCutoff(ctx, "user-2", before) {
		t.Fatal("user without a cutoff should not be revoked")
	}
}

// TestFailOpenOnRedisDown verifies the read path degrades to "not denied"
// when Redis is unreachable, rather than locking users out.
func TestFailOpenOnRedisDown(t *testing.T) {
	s, mr := newTestService(t)
	ctx := context.Background()
	mr.Close() // simulate an outage

	if s.IsJTIDenied(ctx, "jti-1") {
		t.Fatal("IsJTIDenied must fail open (return false) on Redis outage")
	}
	if s.IsIssuedBeforeCutoff(ctx, "user-1", time.Now()) {
		t.Fatal("IsIssuedBeforeCutoff must fail open (return false) on Redis outage")
	}
}
