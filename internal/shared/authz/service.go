// Package authz centralises the runtime lookup + caching of a user's
// effective permission set.
//
// Why this exists:
//   - Historically permissions were embedded inside the JWT claims. That
//     pushed token sizes past 4 KB on users with many permissions, which
//     tripped ingress / CDN header limits and forced a full token
//     regenerate on every revoke.
//   - Keeping permissions in Redis (keyed by user_id) lets
//     the JWT stay small and lets admin-side changes (role edits, role
//     assignments) take effect on the next request after invalidation
//     rather than after the JWT expires.
//
// Cache invariants:
//   - Key: perms:user:<user_id>
//   - Value: JSON array of permission codes (e.g. ["finance.contacts:read"]).
//   - TTL: driven by config.Redis.PermissionTTL.
//   - Empty permission sets are cached too — they represent a valid
//     answer ("this user has nothing") and skipping the cache would
//     hammer the DB for users whose roles grant no explicit perms.
package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"venturo-skeleton-go-simple/pkg/logger"

	goredis "github.com/redis/go-redis/v9"
)

// PermissionFetcher is the narrow seam the cache needs from the role
// layer. role.RoleRepository already implements this signature — we
// accept an interface so the authz package stays decoupled from the
// rest of the role module and is trivial to fake in tests.
type PermissionFetcher interface {
	GetUserPermissions(ctx context.Context, userID string) ([]string, error)
}

// Service is the cached permission lookup. Construct via NewService and
// hand the singleton to middleware + handlers that need to answer
// "does this user have permission X?".
type Service struct {
	redis   *goredis.Client
	fetcher PermissionFetcher
	ttl     time.Duration
}

// NewService wires a PermissionFetcher (the DB source of truth) and a
// Redis client (the cache) into a ready-to-use authz.Service.
func NewService(redis *goredis.Client, fetcher PermissionFetcher, ttl time.Duration) *Service {
	return &Service{
		redis:   redis,
		fetcher: fetcher,
		ttl:     ttl,
	}
}

// cacheKey encodes the per-user cache entry.
func cacheKey(userID string) string {
	return fmt.Sprintf("perms:user:%s", userID)
}

// GetPermissions returns the effective permission codes for userID.
// Cache-first; on miss it falls back to the fetcher and populates Redis.
//
// Redis errors are swallowed (not fatal): if Redis is down we still
// answer correctly from the DB, we just lose the speed-up. That keeps
// an outage in the cache layer from cascading into an auth outage.
func (s *Service) GetPermissions(ctx context.Context, userID string) ([]string, error) {
	if userID == "" {
		return nil, fmt.Errorf("authz: empty userID")
	}

	key := cacheKey(userID)

	// 1. Try cache.
	if raw, err := s.redis.Get(ctx, key).Bytes(); err == nil {
		var perms []string
		if jsonErr := json.Unmarshal(raw, &perms); jsonErr == nil {
			return perms, nil
		}
		// Poisoned entry — drop it and fall through to DB.
		logger.Warn("authz: corrupt cache entry, deleting", logger.String("key", key))
		_ = s.redis.Del(ctx, key).Err()
	} else if err != goredis.Nil {
		logger.Warn("authz: redis GET failed, falling back to DB", logger.Err(err))
	}

	// 2. Cache miss → fetch from DB.
	perms, err := s.fetcher.GetUserPermissions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("authz: fetch user permissions: %w", err)
	}
	if perms == nil {
		// Normalise nil vs empty so the cached payload is always valid JSON.
		perms = []string{}
	}

	// 3. Populate cache (best-effort).
	if payload, mErr := json.Marshal(perms); mErr == nil {
		if setErr := s.redis.Set(ctx, key, payload, s.ttl).Err(); setErr != nil {
			logger.Warn("authz: redis SET failed", logger.Err(setErr))
		}
	}

	return perms, nil
}

// Has is a convenience predicate over GetPermissions.
func (s *Service) Has(ctx context.Context, userID, permission string) (bool, error) {
	perms, err := s.GetPermissions(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, p := range perms {
		if p == permission {
			return true, nil
		}
	}
	return false, nil
}

// HasAny returns true if the user has at least one of the given permissions.
func (s *Service) HasAny(ctx context.Context, userID string, permissions ...string) (bool, error) {
	perms, err := s.GetPermissions(ctx, userID)
	if err != nil {
		return false, err
	}
	set := make(map[string]struct{}, len(perms))
	for _, p := range perms {
		set[p] = struct{}{}
	}
	for _, need := range permissions {
		if _, ok := set[need]; ok {
			return true, nil
		}
	}
	return false, nil
}

// HasAll returns true only if every required permission is present.
func (s *Service) HasAll(ctx context.Context, userID string, permissions ...string) (bool, error) {
	perms, err := s.GetPermissions(ctx, userID)
	if err != nil {
		return false, err
	}
	set := make(map[string]struct{}, len(perms))
	for _, p := range perms {
		set[p] = struct{}{}
	}
	for _, need := range permissions {
		if _, ok := set[need]; !ok {
			return false, nil
		}
	}
	return true, nil
}

// InvalidateUser removes the cached permission set for a user. Call this
// when the user's role assignments change.
func (s *Service) InvalidateUser(ctx context.Context, userID string) error {
	return s.redis.Del(ctx, cacheKey(userID)).Err()
}

// InvalidateAll nukes every cached permission set. Call this when a
// role's permission list itself changes (because any user holding that
// role has a stale set) — cheaper than walking role→users when the
// user count is high.
func (s *Service) InvalidateAll(ctx context.Context) error {
	return s.deleteByPattern(ctx, "perms:user:*")
}

// deleteByPattern iterates keys matching pattern in batches via SCAN
// (never KEYS — that's O(N) blocking on the whole keyspace) and DELs
// them. Safe to run on a shared Redis.
func (s *Service) deleteByPattern(ctx context.Context, pattern string) error {
	var cursor uint64
	for {
		keys, next, err := s.redis.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("authz: redis SCAN: %w", err)
		}
		if len(keys) > 0 {
			if err := s.redis.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("authz: redis DEL: %w", err)
			}
		}
		if next == 0 {
			return nil
		}
		cursor = next
	}
}
