package middleware

import (
	"context"
	"net/http"
	"strings"

	"venturo-skeleton-go/internal/shared/response"
	jwtpkg "venturo-skeleton-go/pkg/jwt"
	"venturo-skeleton-go/pkg/logger"

	"github.com/gin-gonic/gin"
)

// ApiKeyValidator is the interface for validating API keys
type ApiKeyValidator interface {
	ValidateAndGetClaims(ctx context.Context, keyID, secret, clientIP string) (*jwtpkg.Claims, error)
}

// apiKeyValidator is the global API key validator (set by router)
var apiKeyValidator ApiKeyValidator

// SetApiKeyValidator sets the global API key validator
func SetApiKeyValidator(v ApiKeyValidator) {
	apiKeyValidator = v
}

// JWTAuth is a middleware that validates JWT tokens or API keys
func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check for API Key first (X-API-Key header)
		apiKey := c.GetHeader("X-API-Key")
		if apiKey != "" {
			if handleApiKeyAuth(c, apiKey) {
				c.Next()
				return
			}
			c.Abort()
			return
		}

		// Fall back to JWT Bearer token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			logger.Warn("Missing authorization header")
			response.Error(c, http.StatusUnauthorized, "Unauthorized", "Authorization header required")
			c.Abort()
			return
		}

		// Check if header has "Bearer " prefix
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			logger.Warn("Invalid authorization header format")
			response.Error(c, http.StatusUnauthorized, "Unauthorized", "Invalid authorization header format. Expected: Bearer <token>")
			c.Abort()
			return
		}

		tokenString := parts[1]

		// Parse and validate token
		claims, err := jwtpkg.ParseToken(tokenString)
		if err != nil {
			logger.Warn("Invalid token", logger.Err(err))

			// Return specific error message based on error type
			var message string
			switch err {
			case jwtpkg.ErrExpiredToken:
				message = "Token has expired"
			case jwtpkg.ErrInvalidSignature:
				message = "Invalid token signature"
			default:
				message = "Invalid token"
			}

			response.Error(c, http.StatusUnauthorized, "Unauthorized", message)
			c.Abort()
			return
		}

		// Store claims in context for later use
		SetUserContext(c, claims)
		SetAuthType(c, "jwt")

		logger.Info("User authenticated via JWT",
			logger.String("user_id", claims.UserID),
			logger.String("email", claims.Email),
		)

		c.Next()
	}
}

// handleApiKeyAuth handles API key authentication
// Returns true if authentication was successful, false otherwise
func handleApiKeyAuth(c *gin.Context, apiKey string) bool {
	// Check if validator is set
	if apiKeyValidator == nil {
		logger.Warn("API key authentication attempted but no validator is configured")
		response.Error(c, http.StatusUnauthorized, "Unauthorized", "API key authentication is not configured")
		return false
	}

	// Parse API key format: wiz_<env>_<key_id>_<secret>
	parts := strings.Split(apiKey, "_")
	if len(parts) != 4 || parts[0] != "wiz" {
		logger.Warn("Invalid API key format")
		response.Error(c, http.StatusUnauthorized, "Unauthorized", "Invalid API key format")
		return false
	}

	// env := parts[1] // "live" or "test"
	keyID := parts[2]
	secret := parts[3]

	// Validate API key and get claims
	claims, err := apiKeyValidator.ValidateAndGetClaims(c.Request.Context(), keyID, secret, c.ClientIP())
	if err != nil {
		logger.Warn("API key validation failed",
			logger.String("key_id", keyID),
			logger.Err(err),
		)
		response.Error(c, http.StatusUnauthorized, "Unauthorized", "")
		return false
	}

	// Store claims in context
	SetUserContext(c, claims)
	SetAuthType(c, "api_key")
	SetApiKeyID(c, keyID)

	logger.Info("User authenticated via API key",
		logger.String("user_id", claims.UserID),
		logger.String("key_id", keyID),
	)

	return true
}

// OptionalAuth is a middleware that extracts JWT if present but doesn't require it
// Useful for endpoints that work differently for authenticated vs unauthenticated users
func OptionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// No token provided, continue without authentication
			c.Next()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			// Invalid format, continue without authentication
			c.Next()
			return
		}

		tokenString := parts[1]
		claims, err := jwtpkg.ParseToken(tokenString)
		if err != nil {
			// Invalid token, continue without authentication
			c.Next()
			return
		}

		// Valid token, store claims in context
		SetUserContext(c, claims)

		logger.Info("Optional auth: User authenticated",
			logger.String("user_id", claims.UserID),
		)

		c.Next()
	}
}
