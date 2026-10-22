package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go-simple/internal/middleware"
	"venturo-skeleton-go-simple/internal/modules/core/auth/dto"
	"venturo-skeleton-go-simple/internal/modules/core/auth/service"
	"venturo-skeleton-go-simple/internal/shared/response"
)

type AuthHandler struct {
	authService *service.AuthService
}

func NewAuthHandler(authService *service.AuthService) *AuthHandler {
	return &AuthHandler{
		authService: authService,
	}
}

func (h *AuthHandler) SignUp(c *gin.Context) {
	var req dto.SignUpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", err.Error())
		return
	}

	ctx := c.Request.Context()
	result, err := h.authService.SignUp(ctx, &req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrEmailAlreadyExists):
			response.Error(c, http.StatusConflict, "Email already exists", "")
		case errors.Is(err, service.ErrUsernameAlreadyExists):
			response.Error(c, http.StatusConflict, "Username already exists", "")
		default:
			response.Error(c, http.StatusInternalServerError, "Failed to register user", err.Error())
		}
		return
	}

	response.Success(c, http.StatusCreated, result.Message, result)
}

func (h *AuthHandler) SignIn(c *gin.Context) {
	var req dto.SignInRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", err.Error())
		return
	}

	// Get device info from user agent
	userAgent := c.GetHeader("User-Agent")
	deviceInfo := service.GetDeviceInfoFromUserAgent(userAgent)

	// Get IP address
	ipAddress := c.ClientIP()

	ctx := c.Request.Context()
	result, err := h.authService.SignIn(ctx, &req, deviceInfo, ipAddress)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidCredentials):
			response.Error(c, http.StatusUnauthorized, "Invalid credentials", "")
		case errors.Is(err, service.ErrUserNotActive):
			response.Error(c, http.StatusUnauthorized, "User account is not active", "")
		case errors.Is(err, service.ErrUserLocked):
			response.Error(c, http.StatusUnauthorized, "User account is locked. Try again later.", "")
		case errors.Is(err, service.ErrEmailNotVerified):
			response.Error(c, http.StatusUnauthorized, "Email not verified", "")
		default:
			response.Error(c, http.StatusInternalServerError, "Failed to sign in", err.Error())
		}
		return
	}

	response.Success(c, http.StatusOK, "Sign in successful", result)
}

// SignInWithGoogle handles POST /core/v1/auth/google.
//
// Body: { id_token: <Firebase ID token from FE> }
//
// Same response shape as SignIn, with one extra field:
//   - is_new_user (bool): true if this request provisioned a brand-new
//     user (with no role yet); false if the user already existed (either had the identity linked
//     or had a local account with the same email that got auto-linked
//     on this request).
//
// 401 categories:
//   - "Invalid Google ID token"       → Firebase rejected the token
//     (bad signature, expired, wrong audience, revoked).
//   - "Unexpected sign-in provider"   → token came from a non-Google
//     Firebase flow (email link, custom auth, …); refuse so this
//     endpoint stays a Google-only seam.
//   - "Google account did not return an email" → Firebase token had no
//     email claim. Should not happen for Google but guarded anyway.
//   - "User account is not active"    → user was deactivated.
//
// 503 is returned when Firebase is not configured (the operator did
// not set FIREBASE_PROJECT_ID) — the FE can show a "Google sign-in is
// temporarily unavailable" UI instead of failing silently.
func (h *AuthHandler) SignInWithGoogle(c *gin.Context) {
	var req dto.GoogleSignInRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", err.Error())
		return
	}

	userAgent := c.GetHeader("User-Agent")
	deviceInfo := service.GetDeviceInfoFromUserAgent(userAgent)
	ipAddress := c.ClientIP()

	ctx := c.Request.Context()
	result, err := h.authService.SignInWithGoogle(ctx, &req, deviceInfo, ipAddress)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrFirebaseNotConfigured):
			response.Error(c, http.StatusServiceUnavailable, "Google sign-in is not configured", "")
		case errors.Is(err, service.ErrInvalidGoogleToken):
			response.Error(c, http.StatusUnauthorized, "Invalid Google ID token", "")
		case errors.Is(err, service.ErrUnexpectedProvider):
			response.Error(c, http.StatusUnauthorized, "Unexpected sign-in provider", "")
		case errors.Is(err, service.ErrGoogleEmailMissing):
			response.Error(c, http.StatusUnauthorized, "Google account did not return an email", "")
		case errors.Is(err, service.ErrUserNotActive):
			response.Error(c, http.StatusUnauthorized, "User account is not active", "")
		default:
			response.Error(c, http.StatusInternalServerError, "Failed to sign in with Google", err.Error())
		}
		return
	}

	status := http.StatusOK
	msg := "Sign in with Google successful"
	if result.IsNewUser {
		status = http.StatusCreated
		msg = "Account created and signed in with Google"
	}
	response.Success(c, status, msg, result)
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	var req dto.RefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", err.Error())
		return
	}

	ctx := c.Request.Context()
	result, err := h.authService.RefreshToken(ctx, req.RefreshToken)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidRefreshToken):
			response.Error(c, http.StatusUnauthorized, "Invalid refresh token", "")
		case errors.Is(err, service.ErrRefreshTokenExpired):
			response.Error(c, http.StatusUnauthorized, "Refresh token expired", "")
		case errors.Is(err, service.ErrRefreshTokenRevoked):
			response.Error(c, http.StatusUnauthorized, "Refresh token revoked", "")
		case errors.Is(err, service.ErrUserNotActive):
			response.Error(c, http.StatusUnauthorized, "User account is not active", "")
		default:
			response.Error(c, http.StatusInternalServerError, "Failed to refresh token", err.Error())
		}
		return
	}

	response.Success(c, http.StatusOK, "Token refreshed successfully", result)
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var req dto.LogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload", err.Error())
		return
	}

	ctx := c.Request.Context()
	err := h.authService.Logout(ctx, req.RefreshToken)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "Failed to logout", err.Error())
		return
	}

	response.Success(c, http.StatusOK, "Logged out successfully", nil)
}

func (h *AuthHandler) LogoutAll(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	ctx := c.Request.Context()
	err := h.authService.LogoutAll(ctx, userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "Failed to logout from all devices", err.Error())
		return
	}

	response.Success(c, http.StatusOK, "Logged out from all devices successfully", nil)
}

func (h *AuthHandler) GetMe(c *gin.Context) {
	claims := middleware.MustGetUserFromContext(c)

	ctx := c.Request.Context()
	result, err := h.authService.GetMe(ctx, claims.UserID, claims.IsSuperAdmin)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrUserNotActive):
			response.Error(c, http.StatusUnauthorized, "User not active", err.Error())
		default:
			response.Error(c, http.StatusInternalServerError, "Failed to load profile", err.Error())
		}
		return
	}

	response.Success(c, http.StatusOK, "Profile retrieved successfully", result)
}
