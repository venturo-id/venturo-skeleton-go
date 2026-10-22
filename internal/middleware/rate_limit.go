package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"venturo-skeleton-go-simple/internal/shared/response"

	"github.com/gin-gonic/gin"
)

// RateLimiter manages rate limiting for requests
type RateLimiter struct {
	visitors map[string]*Visitor
	mu       sync.RWMutex
	rate     int           // requests per window
	window   time.Duration // time window
}

// Visitor tracks request attempts for a specific identifier (IP or user)
type Visitor struct {
	lastSeen  time.Time
	attempts  int
	lockedUntil time.Time
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*Visitor),
		rate:     rate,
		window:   window,
	}

	// Start cleanup goroutine to remove old entries
	go rl.cleanupVisitors()

	return rl
}

// cleanupVisitors removes old visitor entries every minute
func (rl *RateLimiter) cleanupVisitors() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		for key, visitor := range rl.visitors {
			// Remove visitors that haven't been seen in the last 10 minutes
			// and are not locked
			if time.Since(visitor.lastSeen) > 10*time.Minute && time.Now().After(visitor.lockedUntil) {
				delete(rl.visitors, key)
			}
		}
		rl.mu.Unlock()
	}
}

// getVisitor retrieves or creates a visitor record
func (rl *RateLimiter) getVisitor(identifier string) *Visitor {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	visitor, exists := rl.visitors[identifier]
	if !exists {
		visitor = &Visitor{
			lastSeen:  time.Now(),
			attempts:  0,
			lockedUntil: time.Time{},
		}
		rl.visitors[identifier] = visitor
	}

	return visitor
}

// Allow checks if a request should be allowed
func (rl *RateLimiter) Allow(identifier string, lockoutDuration time.Duration) (allowed bool, retryAfter time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	visitor := rl.visitors[identifier]
	if visitor == nil {
		visitor = &Visitor{
			lastSeen:  time.Now(),
			attempts:  0,
			lockedUntil: time.Time{},
		}
		rl.visitors[identifier] = visitor
	}

	now := time.Now()

	// Check if account is locked
	if now.Before(visitor.lockedUntil) {
		return false, time.Until(visitor.lockedUntil)
	}

	// Reset attempts if window has passed
	if now.Sub(visitor.lastSeen) > rl.window {
		visitor.attempts = 0
	}

	// Increment attempts
	visitor.attempts++
	visitor.lastSeen = now

	// Check if exceeded rate limit
	if visitor.attempts > rl.rate {
		// Lock the account/IP
		visitor.lockedUntil = now.Add(lockoutDuration)
		return false, lockoutDuration
	}

	return true, 0
}

// Reset clears the attempts for an identifier
func (rl *RateLimiter) Reset(identifier string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if visitor, exists := rl.visitors[identifier]; exists {
		visitor.attempts = 0
		visitor.lockedUntil = time.Time{}
	}
}

// RateLimitMiddleware creates a middleware for rate limiting
func RateLimitMiddleware(limiter *RateLimiter, lockoutDuration time.Duration, identifierFunc func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		identifier := identifierFunc(c)

		allowed, retryAfter := limiter.Allow(identifier, lockoutDuration)
		if !allowed {
			c.Header("X-RateLimit-Limit", string(rune(limiter.rate)))
			c.Header("X-RateLimit-Retry-After", retryAfter.String())

			if retryAfter > 0 {
				response.Error(c, http.StatusTooManyRequests,
					"Too many attempts. Please try again later.",
					fmt.Sprintf("Retry after %d seconds", int(retryAfter.Seconds())))
			} else {
				response.Error(c, http.StatusTooManyRequests,
					"Rate limit exceeded. Please try again later.",
					"")
			}

			c.Abort()
			return
		}

		c.Next()
	}
}

// IPBasedRateLimiter creates a rate limiter based on IP address
func IPBasedRateLimiter(limiter *RateLimiter, lockoutDuration time.Duration) gin.HandlerFunc {
	return RateLimitMiddleware(limiter, lockoutDuration, func(c *gin.Context) string {
		return c.ClientIP()
	})
}

// CombinedRateLimiter creates a rate limiter based on both IP and user identifier
// This is recommended for login endpoints to prevent both IP-based and account-targeted attacks
func CombinedRateLimiter(limiter *RateLimiter, lockoutDuration time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get IP address
		ip := c.ClientIP()

		// Try to get user identifier from request body without consuming it
		var identifier string

		// Read the body
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err == nil && len(bodyBytes) > 0 {
			// Restore the body for the handler to read later
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			// Try to parse JSON to extract identifier
			var loginRequest struct {
				Login string `json:"login"`
				Email string `json:"email"`
			}

			// Parse JSON without affecting the original body
			if json.Unmarshal(bodyBytes, &loginRequest) == nil {
				identifier = loginRequest.Login
				if identifier == "" {
					identifier = loginRequest.Email
				}
			}
		}

		// Check IP-based rate limit
		ipAllowed, ipRetryAfter := limiter.Allow("ip:"+ip, lockoutDuration)
		if !ipAllowed {
			c.Header("X-RateLimit-Retry-After", ipRetryAfter.String())
			response.Error(c, http.StatusTooManyRequests,
				"Too many requests from your IP address. Please try again later.",
				fmt.Sprintf("Retry after %d seconds", int(ipRetryAfter.Seconds())))
			c.Abort()
			return
		}

		// Check user-based rate limit if identifier is available
		if identifier != "" {
			userAllowed, userRetryAfter := limiter.Allow("user:"+identifier, lockoutDuration)
			if !userAllowed {
				c.Header("X-RateLimit-Retry-After", userRetryAfter.String())
				response.Error(c, http.StatusTooManyRequests,
					"Too many login attempts for this account. Please try again later.",
					fmt.Sprintf("Retry after %d seconds", int(userRetryAfter.Seconds())))
				c.Abort()
				return
			}
		}

		c.Next()
	}
}
