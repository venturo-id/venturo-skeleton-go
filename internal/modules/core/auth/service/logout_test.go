package service

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	pkgjwt "venturo-skeleton-go/pkg/jwt"
)

func TestAccessTokenDenyParams(t *testing.T) {
	now := time.Now()

	t.Run("valid claims yield jti and positive ttl", func(t *testing.T) {
		claims := &pkgjwt.Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				ID:        "jti-123",
				ExpiresAt: jwt.NewNumericDate(now.Add(30 * time.Minute)),
			},
		}
		jti, ttl, ok := accessTokenDenyParams(claims)
		if !ok {
			t.Fatal("expected ok=true for valid claims")
		}
		if jti != "jti-123" {
			t.Fatalf("jti = %q, want jti-123", jti)
		}
		if ttl <= 0 || ttl > 30*time.Minute {
			t.Fatalf("ttl = %v, want (0, 30m]", ttl)
		}
	})

	t.Run("nil claims rejected", func(t *testing.T) {
		if _, _, ok := accessTokenDenyParams(nil); ok {
			t.Fatal("expected ok=false for nil claims")
		}
	})

	t.Run("missing jti rejected", func(t *testing.T) {
		claims := &pkgjwt.Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			},
		}
		if _, _, ok := accessTokenDenyParams(claims); ok {
			t.Fatal("expected ok=false when jti is empty")
		}
	})

	t.Run("already expired rejected", func(t *testing.T) {
		claims := &pkgjwt.Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				ID:        "jti-expired",
				ExpiresAt: jwt.NewNumericDate(now.Add(-time.Minute)),
			},
		}
		if _, _, ok := accessTokenDenyParams(claims); ok {
			t.Fatal("expected ok=false for an already-expired token")
		}
	})
}
