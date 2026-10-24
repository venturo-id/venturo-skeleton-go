package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"timebox-be/internal/shared/audit"
	"timebox-be/internal/shared/authz"
	"timebox-be/internal/shared/ratelimit"
	"timebox-be/internal/shared/response"
	jwtpkg "timebox-be/pkg/jwt"
	"timebox-be/pkg/logger"

	"github.com/gin-gonic/gin"
)

// APIKeyValidator checks an API key's secret, status, IP whitelist and
// rate limit, and returns claims naming the owner and carrying the key's
// stored scope in ScopedPermissions.
type APIKeyValidator interface {
	ValidateAndGetClaims(ctx context.Context, environment, keyID, secret, clientIP string) (*jwtpkg.Claims, error)
}

// SubjectReader returns a user's live authorization state (implemented by
// *authz.Service).
type SubjectReader interface {
	GetSubject(ctx context.Context, userID string) (authz.Subject, error)
}

// Error codes of a request refused for its credential. Only
// CodeTokenExpired is fixed by refreshing the access token; after
// CodeSessionEnded and CodeUserInactive the client signs in again.
const (
	CodeTokenExpired = "token_expired"
	CodeSessionEnded = "session_ended"
	CodeUserInactive = "user_inactive"
)

// apiKeyValidator is the global API key validator (set by router)
var apiKeyValidator APIKeyValidator

// authzService is the live authorization backend used by JWTAuth. It is
// wired in at bootstrap via SetAuthzService; until then, every
// authenticated request is rejected with 500 rather than silently passing.
var authzService SubjectReader

// SetAPIKeyValidator sets the global API key validator
func SetAPIKeyValidator(v APIKeyValidator) {
	apiKeyValidator = v
}

// SetAuthzService registers the authorization backend. Call this once
// during router setup, after Redis is initialised.
func SetAuthzService(s SubjectReader) {
	authzService = s
}

// JWTAuth is a middleware that validates JWT tokens or API keys.
//
// Besides checking the credential it loads the caller's live authz
// subject: a deactivated or deleted user is rejected here even while
// their token is still unexpired, and so is a token issued before the
// user last signed out everywhere. The permission middleware reads the
// subject instead of trusting claims baked into the token.
func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check for API Key first (X-API-Key header)
		if apiKey := c.GetHeader("X-API-Key"); apiKey != "" {
			if !handleAPIKeyAuth(c, apiKey) {
				c.Abort()
				return
			}
			c.Next()
			return
		}

		// Fall back to JWT Bearer token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			response.Error(c, http.StatusUnauthorized, "Unauthorized", "Authorization header required")
			c.Abort()
			return
		}

		scheme, tokenString, found := strings.Cut(authHeader, " ")
		if !found || scheme != "Bearer" {
			response.Error(c, http.StatusUnauthorized, "Unauthorized", "Invalid authorization header format. Expected: Bearer <token>")
			c.Abort()
			return
		}

		claims, err := jwtpkg.ParseToken(tokenString)
		if err != nil {
			logger.Debug("Invalid token", logger.Err(err))

			// An expired token is the one case a refresh fixes.
			code, message := response.CodeUnauthorized, "Invalid token"
			switch {
			case errors.Is(err, jwtpkg.ErrExpiredToken):
				code, message = CodeTokenExpired, "Token has expired"
			case errors.Is(err, jwtpkg.ErrInvalidSignature):
				message = "Invalid token signature"
			}

			response.ErrorCode(c, http.StatusUnauthorized, code, "Unauthorized", message)
			c.Abort()
			return
		}

		subject, ok := loadActiveSubject(c, claims.UserID)
		if !ok {
			c.Abort()
			return
		}

		if !subject.AcceptsTokenIssuedAt(claims.IssuedAt.Time) || !subject.AcceptsSession(claims.SessionID) {
			logger.Debug("Rejected token of an ended session",
				logger.String("user_id", claims.UserID))
			response.ErrorCode(c, http.StatusUnauthorized, CodeSessionEnded, "Unauthorized", "Session has been revoked. Please sign in again.")
			c.Abort()
			return
		}

		SetUserContext(c, claims)
		SetSubject(c, subject)
		SetAuthType(c, AuthTypeJWT)
		c.Request = c.Request.WithContext(audit.WithActor(c.Request.Context(), claims.UserID, ""))

		c.Next()
	}
}

// handleAPIKeyAuth authenticates an X-API-Key. When it returns false the
// error response has been written.
func handleAPIKeyAuth(c *gin.Context, apiKey string) bool {
	if apiKeyValidator == nil {
		logger.Error("API key authentication attempted but no validator is configured")
		response.Error(c, http.StatusInternalServerError, "Server misconfiguration", "API key authentication is not configured")
		return false
	}

	// Format: tbx_<env>_<key_id>_<secret>
	parts := strings.Split(apiKey, "_")
	if len(parts) != 4 || parts[0] != authz.APIKeyPrefix || parts[1] == "" || parts[2] == "" || parts[3] == "" {
		response.Error(c, http.StatusUnauthorized, "Unauthorized", "Invalid API key format")
		return false
	}
	environment, keyID, secret := parts[1], parts[2], parts[3]

	claims, err := apiKeyValidator.ValidateAndGetClaims(c.Request.Context(), environment, keyID, secret, c.ClientIP())
	if err != nil {
		var exceeded *ratelimit.ExceededError
		if errors.As(err, &exceeded) {
			logger.Warn("API key rate limit exceeded", logger.String("key_id", keyID))
			writeTooManyRequests(c, exceeded.RetryAfter)
			return false
		}

		var rejected *authz.CredentialError
		if errors.As(err, &rejected) {
			logger.Warn("API key rejected",
				logger.String("key_id", keyID),
				logger.Err(err),
			)
			response.Error(c, http.StatusUnauthorized, "Unauthorized", rejected.Reason)
			return false
		}

		logger.Error("API key validation failed",
			logger.String("key_id", keyID),
			logger.Err(err),
		)
		response.Error(c, http.StatusInternalServerError, "Failed to validate API key", "")
		return false
	}

	// The key is only as good as its owner's account.
	owner, ok := loadActiveSubject(c, claims.UserID)
	if !ok {
		return false
	}

	// An API key acts with its own scope, narrowed to what the owner
	// holds right now, and never as super admin, even when its owner is one.
	SetUserContext(c, claims)
	SetSubject(c, authz.Subject{Active: true, Permissions: heldScope(claims.ScopedPermissions, owner)})
	SetAuthType(c, AuthTypeAPIKey)
	SetAPIKeyID(c, keyID)
	c.Request = c.Request.WithContext(audit.WithActor(c.Request.Context(), claims.UserID, keyID))

	return true
}

// heldScope keeps the scope entries the owner still holds. A key created
// while its owner had a permission stops using it once the owner loses it.
func heldScope(scope []string, owner authz.Subject) []string {
	held := make([]string, 0, len(scope))
	for _, p := range scope {
		if owner.Has(p) {
			held = append(held, p)
		}
	}
	return held
}

// loadActiveSubject fetches the user's live authz subject. When the
// request must stop it writes the error response and returns false.
func loadActiveSubject(c *gin.Context, userID string) (authz.Subject, bool) {
	if authzService == nil {
		logger.Error("authz service not initialised — cannot authenticate")
		response.Error(c, http.StatusInternalServerError, "Server misconfiguration", "Permission backend unavailable")
		return authz.Subject{}, false
	}

	subject, err := authzService.GetSubject(c.Request.Context(), userID)
	if err != nil {
		logger.Error("Failed to load authorization state",
			logger.String("user_id", userID),
			logger.Err(err),
		)
		response.Error(c, http.StatusInternalServerError, "Permission lookup failed", "")
		return authz.Subject{}, false
	}

	if !subject.Active {
		logger.Warn("Rejected request from inactive user", logger.String("user_id", userID))
		response.ErrorCode(c, http.StatusUnauthorized, CodeUserInactive, "Unauthorized", "User account is not active")
		return authz.Subject{}, false
	}

	return subject, true
}

// DenyAPIKeyAuth rejects requests authenticated with an API key. Use it
// after JWTAuth on endpoints that manage credentials, sessions or the
// caller's own account: a narrowly scoped key must not be able to mint a
// broader key, change its owner's password or profile, or sign its owner
// out.
func DenyAPIKeyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if IsAPIKeyAuth(c) {
			logger.Warn("API key used on a session-only endpoint",
				logger.String("key_id", GetAPIKeyID(c)),
				logger.String("path", c.FullPath()),
			)
			response.Error(c, http.StatusForbidden, "Forbidden", "This endpoint cannot be used with an API key")
			c.Abort()
			return
		}
		c.Next()
	}
}
