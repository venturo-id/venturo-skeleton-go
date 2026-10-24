// Package authz centralises the runtime lookup + caching of what a user
// is allowed to do right now.
//
// Why this exists:
//   - Historically permissions were embedded inside the JWT claims. That
//     pushed token sizes past 4 KB on users with many permissions, which
//     tripped ingress / CDN header limits and forced a full token
//     regenerate on every revoke.
//   - Keeping the authorization state in Redis (keyed by user_id) lets
//     the JWT stay small and lets admin-side changes (role edits, role
//     assignments, deactivation, signing out everywhere) take effect on
//     the next request after invalidation rather than after the JWT
//     expires.
//
// Cache invariants:
//   - Key: timebox:perms:user:<user_id>
//   - Value: JSON Subject (active flag, super-admin flag, permission codes
//     such as "users:read", session epoch).
//   - TTL: driven by config.Redis.PermissionTTL.
//   - Inactive users and empty permission sets are cached too — they are
//     valid answers, and skipping the cache would hammer the DB.
//   - Every invalidation bumps a version counter (per user, and one for
//     "all users"). A request that read the DB before an invalidation
//     only writes its result back if the versions are unchanged, so a
//     stale subject cannot outlive the invalidation that should have
//     removed it.
package authz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sharedRedis "timebox-be/internal/shared/redis"
	"timebox-be/pkg/logger"

	goredis "github.com/redis/go-redis/v9"
)

// Subject is the live authorization state of one user. It is the source
// of truth for the request path; the JWT only identifies the user.
type Subject struct {
	// Active is false for deactivated, soft-deleted, or unknown users.
	// An inactive subject must not be served at all.
	Active bool `json:"active"`
	// SuperAdmin is true while the user holds an active super_admin role.
	// It bypasses every permission check.
	SuperAdmin bool `json:"super_admin"`
	// Permissions are the expanded "resource:action" codes merged from
	// the user's active roles. Empty for inactive users.
	Permissions []string `json:"permissions"`
	// TokensValidAfter (unix milliseconds) rejects every access token
	// issued before it. It moves forward when the user signs out
	// everywhere, changes their password, is deactivated, or a stolen
	// refresh token is detected.
	TokensValidAfter int64 `json:"tokens_valid_after_ms,omitempty"`
	// EndedSessions are the sessions that ended recently enough for one
	// of their access tokens to be unexpired: their tokens are refused.
	EndedSessions []string `json:"ended_sessions,omitempty"`
}

// Has reports whether the subject may perform permission.
func (s Subject) Has(permission string) bool {
	if !s.Active {
		return false
	}
	if s.SuperAdmin {
		return true
	}
	for _, p := range s.Permissions {
		if p == permission {
			return true
		}
	}
	return false
}

// Covers reports whether the subject holds every permission in perms.
// Use it as the ceiling for anything that grants access to someone else:
// a caller may only hand out, or manage holders of, permissions it holds.
func (s Subject) Covers(perms []string) bool {
	if !s.Active {
		return false
	}
	if s.SuperAdmin {
		return true
	}
	held := make(map[string]struct{}, len(s.Permissions))
	for _, p := range s.Permissions {
		held[p] = struct{}{}
	}
	for _, need := range perms {
		if _, ok := held[need]; !ok {
			return false
		}
	}
	return true
}

// AcceptsTokenIssuedAt reports whether an access token issued at
// issuedAt is still valid for this subject. Both sides have millisecond
// precision (see pkg/jwt), but a token's issue time is a JSON float that
// can read back 1 ms early. That millisecond is forgiven, so a session
// started right after a revocation (e.g. Google sign-in claiming an
// account) is never refused; only a token minted within 1 ms before the
// revocation could slip through.
func (s Subject) AcceptsTokenIssuedAt(issuedAt time.Time) bool {
	return issuedAt.UnixMilli() >= s.TokensValidAfter-1
}

// AcceptsSession reports whether an access token of session sessionID may
// still be used: false once the session was signed out or ended from
// another device. A token without a session id predates sessions and is
// only checked against TokensValidAfter.
func (s Subject) AcceptsSession(sessionID string) bool {
	for _, ended := range s.EndedSessions {
		if ended == sessionID {
			return false
		}
	}
	return true
}

// SubjectFetcher is the narrow seam the cache needs from the data layer.
// role.RoleRepository implements it — we accept an interface so the authz
// package stays decoupled from the role module and is trivial to fake in
// tests.
type SubjectFetcher interface {
	GetUserSubject(ctx context.Context, userID string) (Subject, error)
}

// Service is the cached subject lookup. Construct via NewService and
// hand the singleton to middleware + handlers that need to answer
// "may this user do X?".
type Service struct {
	redis   *goredis.Client
	fetcher SubjectFetcher
	ttl     time.Duration
}

// NewService wires a SubjectFetcher (the DB source of truth) and a Redis
// client (the cache) into a ready-to-use authz.Service.
func NewService(redis *goredis.Client, fetcher SubjectFetcher, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &Service{
		redis:   redis,
		fetcher: fetcher,
		ttl:     ttl,
	}
}

const (
	keySubjectPrefix = sharedRedis.KeyPrefix + "perms:user:"
	keyVersionPrefix = sharedRedis.KeyPrefix + "perms:ver:"
	keyVersionAll    = sharedRedis.KeyPrefix + "perms:ver:all"
)

func subjectKey(userID string) string { return keySubjectPrefix + userID }
func versionKey(userID string) string { return keyVersionPrefix + userID }

// setIfUnchanged stores the subject only when neither version counter has
// moved since the caller read them, i.e. no invalidation happened while
// the subject was being loaded from the database.
var setIfUnchanged = goredis.NewScript(`
local v = redis.call('MGET', KEYS[2], KEYS[3])
if (v[1] or '0') == ARGV[1] and (v[2] or '0') == ARGV[2] then
	redis.call('SET', KEYS[1], ARGV[3], 'PX', ARGV[4])
	return 1
end
return 0
`)

// GetSubject returns the live authorization state for userID.
// Cache-first; on miss it falls back to the fetcher and populates Redis.
//
// Redis errors are swallowed (not fatal): if Redis is down we still
// answer correctly from the DB, we just lose the speed-up. That keeps
// an outage in the cache layer from cascading into an auth outage.
func (s *Service) GetSubject(ctx context.Context, userID string) (Subject, error) {
	if userID == "" {
		return Subject{}, errors.New("authz: empty userID")
	}

	key := subjectKey(userID)

	// 1. Try cache.
	if raw, err := s.redis.Get(ctx, key).Bytes(); err == nil {
		var subject Subject
		if jsonErr := json.Unmarshal(raw, &subject); jsonErr == nil {
			return subject, nil
		}
		// Poisoned (or pre-Subject format) entry — drop it and fall
		// through to DB.
		logger.Warn("authz: corrupt cache entry, deleting", logger.String("key", key))
		_ = s.redis.Del(ctx, key).Err()
	} else if !errors.Is(err, goredis.Nil) {
		logger.Warn("authz: redis GET failed, falling back to DB", logger.Err(err))
	}

	// 2. Read the versions before the DB, so an invalidation that lands
	// while we are fetching is noticed at write-back time.
	versions, verErr := s.redis.MGet(ctx, versionKey(userID), keyVersionAll).Result()
	if verErr != nil {
		logger.Warn("authz: redis MGET failed", logger.Err(verErr))
	}

	// 3. Cache miss → fetch from DB.
	subject, err := s.fetcher.GetUserSubject(ctx, userID)
	if err != nil {
		return Subject{}, fmt.Errorf("authz: fetch user subject: %w", err)
	}
	if subject.Permissions == nil {
		// Normalise nil vs empty so callers and the cache always see an array.
		subject.Permissions = []string{}
	}

	// 4. Populate cache (best-effort), unless an invalidation raced us.
	if verErr == nil {
		if payload, mErr := json.Marshal(subject); mErr == nil {
			err := setIfUnchanged.Run(ctx, s.redis,
				[]string{key, versionKey(userID), keyVersionAll},
				versionString(versions[0]), versionString(versions[1]), payload, s.ttl.Milliseconds(),
			).Err()
			if err != nil {
				logger.Warn("authz: redis SET failed", logger.Err(err))
			}
		}
	}

	return subject, nil
}

// versionString renders an MGET slot the way the Lua script reads it:
// a missing counter is "0".
func versionString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return "0"
}

// GetPermissions returns the effective permission codes for userID —
// empty for an inactive user.
func (s *Service) GetPermissions(ctx context.Context, userID string) ([]string, error) {
	subject, err := s.GetSubject(ctx, userID)
	if err != nil {
		return nil, err
	}
	return subject.Permissions, nil
}

// InvalidateUser removes the cached state for a user. Call this when the
// user's role assignments, active status or session epoch change.
func (s *Service) InvalidateUser(ctx context.Context, userID string) error {
	pipe := s.redis.TxPipeline()
	pipe.Incr(ctx, versionKey(userID))
	pipe.Expire(ctx, versionKey(userID), s.versionTTL())
	pipe.Del(ctx, subjectKey(userID))
	_, err := pipe.Exec(ctx)
	return err
}

// InvalidateAll nukes every cached subject. Call this when a role's
// permission list itself changes (because any user holding that role has
// a stale set) — cheaper than walking role→users when the user count is
// high.
func (s *Service) InvalidateAll(ctx context.Context) error {
	pipe := s.redis.TxPipeline()
	pipe.Incr(ctx, keyVersionAll)
	pipe.Expire(ctx, keyVersionAll, s.versionTTL())
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("authz: bump version: %w", err)
	}
	return s.deleteByPattern(ctx, keySubjectPrefix+"*")
}

// versionTTL keeps version counters a little longer than any subject
// written against them could live; after that a reset to "0" is harmless.
func (s *Service) versionTTL() time.Duration {
	return 2*s.ttl + time.Minute
}

// deleteByPattern iterates keys matching pattern in batches via SCAN
// (never KEYS — that's O(N) blocking on the whole keyspace) and DELs
// them.
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
