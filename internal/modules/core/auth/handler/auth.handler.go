package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"timebox-be/internal/middleware"
	"timebox-be/internal/modules/core/auth/dto"
	"timebox-be/internal/modules/core/auth/service"
	"timebox-be/internal/shared/request"
	"timebox-be/internal/shared/response"
)

type AuthHandler struct {
	authService *service.AuthService
	cookie      CookieConfig
}

func NewAuthHandler(authService *service.AuthService, cookie CookieConfig) *AuthHandler {
	return &AuthHandler{
		authService: authService,
		cookie:      cookie,
	}
}

// SignUp handles POST /core/v1/auth/signup. No tokens: the account signs
// in by entering the code emailed to it (VerifyEmail).
func (h *AuthHandler) SignUp(c *gin.Context) {
	var req dto.SignUpRequest
	if !request.BindJSON(c, &req) {
		return
	}

	result, err := h.authService.SignUp(c.Request.Context(), &req)
	if err != nil {
		h.handleError(c, err, "Failed to register user")
		return
	}

	response.Success(c, http.StatusCreated, "Account created. Enter the code sent to your email.", result)
}

func (h *AuthHandler) SignIn(c *gin.Context) {
	var req dto.SignInRequest
	if !request.BindJSON(c, &req) {
		return
	}

	deviceInfo := service.GetDeviceInfoFromUserAgent(c.GetHeader("User-Agent"))

	result, err := h.authService.SignIn(c.Request.Context(), &req, deviceInfo, c.ClientIP())
	if err != nil {
		h.handleError(c, err, "Failed to sign in")
		return
	}

	h.respondSession(c, http.StatusOK, "Sign in successful", result)
}

// respondSession answers with a new session. The browser app gets the
// refresh token as a cookie instead of in the body.
func (h *AuthHandler) respondSession(c *gin.Context, status int, message string, result *dto.SignInResponse) {
	result.RefreshToken = h.deliverRefreshToken(c, result.RefreshToken)
	response.Success(c, status, message, result)
}

// GoogleClient handles GET /core/v1/auth/google/client?platform=: the
// OAuth client id the app starts Google's flow with. 503 when the
// platform has no client configured.
func (h *AuthHandler) GoogleClient(c *gin.Context) {
	var query dto.GoogleClientQuery
	if !request.BindQuery(c, &query) {
		return
	}

	result, err := h.authService.GoogleClient(query.Platform)
	if err != nil {
		h.handleError(c, err, "Failed to load Google sign-in")
		return
	}

	response.Success(c, http.StatusOK, "Google client retrieved", result)
}

// SignInWithGoogle handles POST /core/v1/auth/google: the authorization
// code of Google's flow with PKCE, exchanged here.
//
// Same response shape as SignIn, with is_new_user: true (and 201) when
// this request created the account (with no role yet).
func (h *AuthHandler) SignInWithGoogle(c *gin.Context) {
	var req dto.GoogleSignInRequest
	if !request.BindJSON(c, &req) {
		return
	}

	deviceInfo := service.GetDeviceInfoFromUserAgent(c.GetHeader("User-Agent"))

	result, err := h.authService.SignInWithGoogle(c.Request.Context(), &req, deviceInfo, c.ClientIP())
	if err != nil {
		h.handleError(c, err, "Failed to sign in with Google")
		return
	}

	status := http.StatusOK
	msg := "Sign in with Google successful"
	if result.IsNewUser {
		status = http.StatusCreated
		msg = "Account created and signed in with Google"
	}
	result.RefreshToken = h.deliverRefreshToken(c, result.RefreshToken)
	response.Success(c, status, msg, result)
}

// Refresh handles POST /core/v1/auth/refresh. The refresh token comes in
// the body, or — for the browser app, from an allowed origin — from its
// cookie.
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req dto.RefreshTokenRequest
	if !request.BindOptionalJSON(c, &req) {
		return
	}

	refreshToken := req.RefreshToken
	if refreshToken == "" {
		if !h.originAllowed(c) {
			response.ErrorCode(c, http.StatusForbidden, codeOriginNotAllowed, "Refresh not allowed",
				"Send refresh_token, or the cookie from the web app on an allowed origin")
			return
		}
		if refreshToken = cookieRefreshToken(c); refreshToken == "" {
			h.clearRefreshCookie(c)
			response.ErrorCode(c, http.StatusUnauthorized, codeSessionEnded, "No refresh token", "Sign in again")
			return
		}
	}

	result, err := h.authService.RefreshToken(c.Request.Context(), refreshToken)
	if err != nil {
		if isSessionOver(err) {
			h.clearRefreshCookie(c)
		}
		h.handleError(c, err, "Failed to refresh token")
		return
	}

	result.RefreshToken = h.deliverRefreshToken(c, result.RefreshToken)
	response.Success(c, http.StatusOK, "Token refreshed successfully", result)
}

// isSessionOver reports whether a refresh failed because the session is
// over, rather than for a moment.
func isSessionOver(err error) bool {
	return errors.Is(err, service.ErrInvalidRefreshToken) || errors.Is(err, service.ErrRefreshTokenExpired) ||
		errors.Is(err, service.ErrRefreshTokenRevoked) || errors.Is(err, service.ErrUserNotActive) ||
		errors.Is(err, service.ErrEmailNotVerified)
}

// Logout handles POST /core/v1/auth/logout: it ends the session of the
// refresh token in the body or the browser app's cookie, or else the
// session of the access token itself.
func (h *AuthHandler) Logout(c *gin.Context) {
	var req dto.LogoutRequest
	if !request.BindOptionalJSON(c, &req) {
		return
	}

	claims := middleware.MustGetUserFromContext(c)
	refreshToken := req.RefreshToken
	if refreshToken == "" {
		refreshToken = cookieRefreshToken(c)
	}

	var err error
	switch {
	case refreshToken != "":
		err = h.authService.Logout(c.Request.Context(), claims.UserID, refreshToken)
	case claims.SessionID != "":
		err = h.authService.EndSession(c.Request.Context(), claims.UserID, claims.SessionID)
		if errors.Is(err, service.ErrSessionNotFound) {
			err = nil // over already
		}
	}
	h.clearRefreshCookie(c)
	if err != nil {
		h.handleError(c, err, "Failed to logout")
		return
	}

	response.Success(c, http.StatusOK, "Logged out successfully", nil)
}

func (h *AuthHandler) LogoutAll(c *gin.Context) {
	if err := h.authService.LogoutAll(c.Request.Context(), middleware.MustGetUserID(c)); err != nil {
		h.handleError(c, err, "Failed to logout from all devices")
		return
	}

	h.clearRefreshCookie(c)
	response.Success(c, http.StatusOK, "Logged out from all devices successfully", nil)
}

func (h *AuthHandler) GetMe(c *gin.Context) {
	result, err := h.authService.GetMe(c.Request.Context(), middleware.MustGetUserID(c), middleware.Caller(c))
	if err != nil {
		h.handleError(c, err, "Failed to load profile")
		return
	}

	response.Success(c, http.StatusOK, "Profile retrieved successfully", result)
}

// ResendVerification handles POST /core/v1/auth/verify-email/resend. The
// answer is the same whether or not a code went out.
func (h *AuthHandler) ResendVerification(c *gin.Context) {
	var req dto.EmailRequest
	if !request.BindJSON(c, &req) {
		return
	}

	ticket, err := h.authService.ResendVerificationCode(c.Request.Context(), req.Email)
	if err != nil {
		h.handleError(c, err, "Failed to send verification code")
		return
	}

	response.Success(c, http.StatusOK,
		"If an unverified account uses this email, a verification code has been sent to it.", ticket)
}

// VerifyEmail handles POST /core/v1/auth/verify-email: the code verifies
// the address and signs the account in.
func (h *AuthHandler) VerifyEmail(c *gin.Context) {
	var req dto.EmailCodeRequest
	if !request.BindJSON(c, &req) {
		return
	}

	deviceInfo := service.GetDeviceInfoFromUserAgent(c.GetHeader("User-Agent"))

	result, err := h.authService.VerifyEmail(c.Request.Context(), req.Email, req.Code, deviceInfo, c.ClientIP())
	if err != nil {
		h.handleError(c, err, "Failed to verify email")
		return
	}

	h.respondSession(c, http.StatusOK, "Email verified", result)
}

// ForgotPassword handles POST /core/v1/auth/forgot-password. The answer
// is the same whether or not a code went out.
func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var req dto.EmailRequest
	if !request.BindJSON(c, &req) {
		return
	}

	ticket, err := h.authService.ForgotPassword(c.Request.Context(), req.Email)
	if err != nil {
		h.handleError(c, err, "Failed to send password reset code")
		return
	}

	response.Success(c, http.StatusOK,
		"If an account uses this email, a password reset code has been sent to it.", ticket)
}

// VerifyResetCode handles POST /core/v1/auth/reset-password/verify: a
// right code is traded for the reset token.
func (h *AuthHandler) VerifyResetCode(c *gin.Context) {
	var req dto.EmailCodeRequest
	if !request.BindJSON(c, &req) {
		return
	}

	result, err := h.authService.VerifyResetCode(c.Request.Context(), req.Email, req.Code)
	if err != nil {
		h.handleError(c, err, "Failed to verify code")
		return
	}

	response.Success(c, http.StatusOK, "Code verified. Choose a new password.", result)
}

// ResetPassword handles POST /core/v1/auth/reset-password: the new
// password, and a session on this client; every other session ends.
func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var req dto.ResetPasswordRequest
	if !request.BindJSON(c, &req) {
		return
	}

	deviceInfo := service.GetDeviceInfoFromUserAgent(c.GetHeader("User-Agent"))

	result, err := h.authService.ResetPassword(c.Request.Context(), req.ResetToken, req.NewPassword, deviceInfo, c.ClientIP())
	if err != nil {
		h.handleError(c, err, "Failed to reset password")
		return
	}

	h.respondSession(c, http.StatusOK, "Password has been reset", result)
}

// ListSessions handles GET /core/v1/account/sessions.
func (h *AuthHandler) ListSessions(c *gin.Context) {
	claims := middleware.MustGetUserFromContext(c)
	sessions, err := h.authService.ListSessions(c.Request.Context(), claims.UserID, claims.SessionID)
	if err != nil {
		h.handleError(c, err, "Failed to list sessions")
		return
	}

	response.Success(c, http.StatusOK, "Sessions retrieved successfully", sessions)
}

// EndSession handles DELETE /core/v1/account/sessions/:id.
func (h *AuthHandler) EndSession(c *gin.Context) {
	sessionID, ok := request.UUIDParam(c, "id")
	if !ok {
		return
	}

	if err := h.authService.EndSession(c.Request.Context(), middleware.MustGetUserID(c), sessionID); err != nil {
		h.handleError(c, err, "Failed to end session")
		return
	}

	response.Success(c, http.StatusOK, "Session ended", nil)
}

// reauth turns the request's proof into the service's.
func reauth(req dto.ReauthRequest) service.Reauth {
	proof := service.Reauth{Password: req.CurrentPassword}
	if req.Google != nil {
		exchange := req.Google.Exchange()
		proof.Google = &exchange
	}
	return proof
}

// RequestEmailChange handles POST /core/v1/account/email: a code goes to
// the new address.
func (h *AuthHandler) RequestEmailChange(c *gin.Context) {
	var req dto.ChangeEmailRequest
	if !request.BindJSON(c, &req) {
		return
	}

	result, err := h.authService.RequestEmailChange(c.Request.Context(), middleware.MustGetUserID(c),
		req.NewEmail, reauth(req.ReauthRequest))
	if err != nil {
		h.handleError(c, err, "Failed to request email change")
		return
	}

	response.Success(c, http.StatusOK,
		"Enter the code sent to your new email. Until then your account keeps its current address.", result)
}

// ResendEmailChange handles POST /core/v1/account/email/resend.
func (h *AuthHandler) ResendEmailChange(c *gin.Context) {
	result, err := h.authService.ResendEmailChange(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.handleError(c, err, "Failed to resend the code")
		return
	}

	response.Success(c, http.StatusOK, "A new code has been sent to your new email", result)
}

// ConfirmEmailChange handles POST /core/v1/account/email/confirm: the
// code sent to the new address.
func (h *AuthHandler) ConfirmEmailChange(c *gin.Context) {
	var req dto.CodeRequest
	if !request.BindJSON(c, &req) {
		return
	}

	user, err := h.authService.ConfirmEmailChange(c.Request.Context(), middleware.MustGetUserID(c), req.Code)
	if err != nil {
		h.handleError(c, err, "Failed to change email")
		return
	}

	response.Success(c, http.StatusOK, "Email changed successfully", dto.UserResponse{User: *user})
}

// LinkGoogle handles POST /core/v1/account/google/link.
func (h *AuthHandler) LinkGoogle(c *gin.Context) {
	var req dto.LinkGoogleRequest
	if !request.BindJSON(c, &req) {
		return
	}

	user, err := h.authService.LinkGoogle(c.Request.Context(), middleware.MustGetUserID(c),
		req.CurrentPassword, req.Exchange())
	if err != nil {
		h.handleError(c, err, "Failed to link Google account")
		return
	}

	response.Success(c, http.StatusOK, "Google account linked", dto.UserResponse{User: *user})
}

// UnlinkGoogle handles DELETE /core/v1/account/google.
func (h *AuthHandler) UnlinkGoogle(c *gin.Context) {
	user, err := h.authService.UnlinkGoogle(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		h.handleError(c, err, "Failed to unlink Google account")
		return
	}

	response.Success(c, http.StatusOK, "Google account unlinked", dto.UserResponse{User: *user})
}

// CloseAccount handles POST /core/v1/account/delete.
func (h *AuthHandler) CloseAccount(c *gin.Context) {
	var req dto.ReauthRequest
	if !request.BindJSON(c, &req) {
		return
	}

	err := h.authService.CloseAccount(c.Request.Context(), middleware.MustGetUserID(c), reauth(req))
	if err != nil {
		h.handleError(c, err, "Failed to delete account")
		return
	}

	h.clearRefreshCookie(c)
	response.Success(c, http.StatusOK, "Your account has been deleted", nil)
}

// ExportAccount handles GET /core/v1/account/export.
func (h *AuthHandler) ExportAccount(c *gin.Context) {
	claims := middleware.MustGetUserFromContext(c)
	export, err := h.authService.ExportAccount(c.Request.Context(), claims.UserID, claims.SessionID)
	if err != nil {
		h.handleError(c, err, "Failed to export account data")
		return
	}

	response.Success(c, http.StatusOK, "Account data exported", export)
}
