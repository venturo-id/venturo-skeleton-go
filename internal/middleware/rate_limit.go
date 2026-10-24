package middleware

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"timebox-be/internal/shared/response"
	"timebox-be/pkg/logger"

	"github.com/gin-gonic/gin"
)

// RequestLimiter counts requests per key in fixed windows (implemented by
// *ratelimit.Limiter, which keeps the counters in Redis so a limit holds
// across every replica).
type RequestLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
}

// requestLimiter backs RateLimitByIP (set by router).
var requestLimiter RequestLimiter

// SetRateLimiter registers the limiter behind RateLimitByIP.
func SetRateLimiter(l RequestLimiter) {
	requestLimiter = l
}

// RateLimitByIP allows at most limit requests per window from one client
// IP. name gives each guarded route its own bucket.
//
// The client IP is only as trustworthy as the TRUSTED_PROXIES setting:
// behind an ingress that is not trusted, every client shares the
// ingress's IP and therefore one bucket.
func RateLimitByIP(name string, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if requestLimiter == nil {
			c.Next()
			return
		}

		allowed, retryAfter, err := requestLimiter.Allow(c.Request.Context(), name+":"+clientBucket(c.ClientIP()), limit, window)
		if err != nil {
			logger.Warn("Rate limiter unavailable, allowing request", logger.String("bucket", name), logger.Err(err))
		}
		if !allowed {
			logger.Warn("Rate limit exceeded",
				logger.String("bucket", name),
				logger.String("ip", c.ClientIP()),
			)
			writeTooManyRequests(c, retryAfter)
			c.Abort()
			return
		}

		c.Next()
	}
}

// clientBucket is the rate-limit identity of a client IP. An IPv4 address
// counts on its own; an IPv6 address counts for its whole /64, because a
// single host is usually given a /64 and could otherwise rotate through
// addresses for a fresh bucket on every request.
func clientBucket(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return ip
	}
	return prefix.String()
}

// writeTooManyRequests sends a 429 with a Retry-After header in whole seconds.
func writeTooManyRequests(c *gin.Context, retryAfter time.Duration) {
	response.TooManyRequests(c, response.CodeRateLimited,
		"Too many requests. Please try again later.",
		fmt.Sprintf("Retry after %d seconds", response.RetrySeconds(retryAfter)),
		retryAfter)
}
