// Package tokendenylist makes otherwise-stateless JWT access tokens
// revocable. Access tokens are signed and self-validating, so without a
// server-side check a token stays valid until its natural expiry — even
// after the user logs out. This package closes that window using Redis.
//
// Two revocation primitives, both keyed so entries auto-expire (TTL =
// the token's remaining lifetime), keeping Redis bounded with no cleanup
// job:
//
//   - Per-token denial (jti): Logout adds the current access token's jti.
//     A token whose jti is denied is rejected on the next request.
//     Key: denylist:jti:<jti>
//
//   - Per-user issued-before cutoff (iat): LogoutAll stamps a cutoff =
//     now. Any token whose iat predates the cutoff is rejected, which
//     kills every session at once without having to know each jti.
//     Key: denylist:user:<user_id>:cutoff
//
// Fail-open posture: every read swallows Redis errors and reports
// "not denied". A cache outage must not lock out the entire user base;
// the trade-off is a short revocation gap during an outage, which we log
// so it's visible. This mirrors the authz package's fail-open stance.
package tokendenylist

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"venturo-skeleton-go/pkg/logger"

	goredis "github.com/redis/go-redis/v9"
)

// Service is the Redis-backed access-token denylist. Construct via
// NewService and hand the singleton to the auth service (writes) and the
// JWTAuth middleware (reads).
type Service struct {
	redis *goredis.Client
}

// NewService wires a Redis client into a ready-to-use denylist.
func NewService(redis *goredis.Client) *Service {
	return &Service{redis: redis}
}

func jtiKey(jti string) string {
	return "denylist:jti:" + jti
}

func userCutoffKey(userID string) string {
	return "denylist:user:" + userID + ":cutoff"
}

// DenyJTI denies a single access token by its jti for ttl. ttl should be
// the token's remaining lifetime so the entry self-expires exactly when
// the token would have anyway — denying it for longer wastes memory,
// denying it for less reopens the window. A non-positive ttl is a no-op
// (the token is already expired; the JWT parser will reject it).
func (s *Service) DenyJTI(ctx context.Context, jti string, ttl time.Duration) error {
	if jti == "" || ttl <= 0 {
		return nil
	}
	if err := s.redis.Set(ctx, jtiKey(jti), "1", ttl).Err(); err != nil {
		return fmt.Errorf("tokendenylist: deny jti: %w", err)
	}
	return nil
}

// IsJTIDenied reports whether the given jti is on the denylist.
// Fail-open: on a Redis error (or empty jti) it returns false and logs,
// so a cache outage degrades to "no revocation" rather than "deny all".
func (s *Service) IsJTIDenied(ctx context.Context, jti string) bool {
	if jti == "" {
		return false
	}
	n, err := s.redis.Exists(ctx, jtiKey(jti)).Result()
	if err != nil {
		logger.Warn("tokendenylist: redis EXISTS failed, treating jti as allowed", logger.Err(err))
		return false
	}
	return n > 0
}

// DenyUserBefore stamps a per-user cutoff so every access token issued
// before `cutoff` is rejected (used by logout-all). ttl bounds how long
// the cutoff is enforced — pass the max access-token lifetime so the
// stamp outlives any token it needs to reject, then self-expires.
func (s *Service) DenyUserBefore(ctx context.Context, userID string, cutoff time.Time, ttl time.Duration) error {
	if userID == "" || ttl <= 0 {
		return nil
	}
	val := strconv.FormatInt(cutoff.Unix(), 10)
	if err := s.redis.Set(ctx, userCutoffKey(userID), val, ttl).Err(); err != nil {
		return fmt.Errorf("tokendenylist: deny user before: %w", err)
	}
	return nil
}

// IsIssuedBeforeCutoff reports whether a token with the given iat was
// issued before the user's logout-all cutoff (and is therefore revoked).
// Fail-open on Redis errors and when no cutoff is set.
func (s *Service) IsIssuedBeforeCutoff(ctx context.Context, userID string, iat time.Time) bool {
	if userID == "" {
		return false
	}
	raw, err := s.redis.Get(ctx, userCutoffKey(userID)).Result()
	if err != nil {
		if err != goredis.Nil {
			logger.Warn("tokendenylist: redis GET cutoff failed, treating token as valid", logger.Err(err))
		}
		return false
	}
	cutoffUnix, parseErr := strconv.ParseInt(raw, 10, 64)
	if parseErr != nil {
		logger.Warn("tokendenylist: corrupt cutoff value, ignoring", logger.String("user_id", userID))
		return false
	}
	// iat strictly before the cutoff → revoked. Tokens minted at or after
	// the logout-all moment are fresh logins and must keep working.
	return iat.Unix() < cutoffUnix
}
