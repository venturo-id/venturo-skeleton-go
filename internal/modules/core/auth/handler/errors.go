package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"timebox-be/internal/middleware"
	"timebox-be/internal/modules/core/auth/dto"
	"timebox-be/internal/modules/core/auth/service"
	"timebox-be/internal/shared/response"
)

// Error codes of the auth and account endpoints (Response.Code). Clients
// branch on these; they never change meaning. Errors shared with the
// middleware use its codes (session_ended, user_inactive).
const (
	codeInvalidCredentials     = "invalid_credentials"
	codeEmailNotVerified       = "email_not_verified"
	codeEmailTaken             = "email_taken"
	codeUsernameTaken          = "username_taken"
	codeInvalidCode            = "invalid_code"
	codeCodeExpired            = "code_expired"
	codeTooManyAttempts        = "too_many_attempts"
	codeResendCooldown         = "resend_cooldown"
	codeEmailQuotaExceeded     = "email_quota_exceeded"
	codeInvalidResetToken      = "invalid_reset_token"
	codeEmailUnchanged         = "email_unchanged"
	codeEmailChangeNotPending  = "email_change_not_pending"
	codeAccountLocked          = "account_locked"
	codeIncorrectPassword      = "incorrect_password"
	codeNoPassword             = "no_password"
	codeReauthRequired         = "reauth_required"
	codeReauthStale            = "reauth_stale"
	codeGoogleNotConfigured    = "google_not_configured"
	codeGoogleAuthFailed       = "google_auth_failed"
	codeInvalidRedirectURI     = "invalid_redirect_uri"
	codeGoogleEmailNotVerified = "google_email_not_verified"
	codeGoogleAccountConflict  = "google_account_conflict"
	codeGoogleAccountNotLinked = "google_account_not_linked"
	codeGoogleAlreadyLinked    = "google_already_linked"
	codeGoogleAccountInUse     = "google_account_in_use"
	codeGoogleNotLinked        = "google_not_linked"
	codeSessionNotFound        = "session_not_found"
	codeSuperAdminCannotClose  = "super_admin_cannot_close"
	codeOriginNotAllowed       = "origin_not_allowed"

	codeSessionEnded = middleware.CodeSessionEnded
	codeUserInactive = middleware.CodeUserInactive
)

// apiError is how a service error is answered.
type apiError struct {
	status          int
	code            string
	message, detail string
}

// knownErrors maps the service's errors, checked in order with errors.Is.
// Errors that carry data (codes, cooldowns, an unverified email) are
// answered by handleError before this list.
var knownErrors = []struct {
	err error
	apiError
}{
	// One message for every reason, including a lock.
	{service.ErrInvalidCredentials, apiError{http.StatusUnauthorized, codeInvalidCredentials,
		"Invalid credentials. After several failed attempts, sign-in is paused for a while.", ""}},
	{service.ErrUserNotActive, apiError{http.StatusUnauthorized, codeUserInactive, "User account is not active", ""}},
	{service.ErrEmailAlreadyExists, apiError{http.StatusConflict, codeEmailTaken, "Email already exists", ""}},
	{service.ErrUsernameAlreadyExists, apiError{http.StatusConflict, codeUsernameTaken, "Username already exists", ""}},
	{service.ErrInvalidRefreshToken, apiError{http.StatusUnauthorized, codeSessionEnded, "Invalid refresh token", "Sign in again"}},
	{service.ErrRefreshTokenExpired, apiError{http.StatusUnauthorized, codeSessionEnded, "Refresh token expired", "Sign in again"}},
	{service.ErrRefreshTokenRevoked, apiError{http.StatusUnauthorized, codeSessionEnded, "Refresh token revoked", "Sign in again"}},
	{service.ErrInvalidResetToken, apiError{http.StatusBadRequest, codeInvalidResetToken,
		"This reset is invalid or has expired", "Ask for a new code"}},
	{service.ErrGoogleNotConfigured, apiError{http.StatusServiceUnavailable, codeGoogleNotConfigured, "Google sign-in is not configured", ""}},
	{service.ErrInvalidRedirectURI, apiError{http.StatusBadRequest, codeInvalidRedirectURI, "Redirect URI is not allowed", ""}},
	{service.ErrGoogleAuthFailed, apiError{http.StatusUnauthorized, codeGoogleAuthFailed, "Google sign-in failed", "Try signing in with Google again"}},
	{service.ErrGoogleEmailNotVerified, apiError{http.StatusUnauthorized, codeGoogleEmailNotVerified, "Google account email is not verified", ""}},
	{service.ErrGoogleAccountConflict, apiError{http.StatusConflict, codeGoogleAccountConflict,
		"The account with this email signs in with another Google account", "Sign in with that Google account or with your password"}},
	{service.ErrGoogleAlreadyLinked, apiError{http.StatusConflict, codeGoogleAlreadyLinked,
		"A Google account is already linked", "Unlink it first"}},
	{service.ErrGoogleAccountInUse, apiError{http.StatusConflict, codeGoogleAccountInUse,
		"This Google account is linked to another account", ""}},
	{service.ErrGoogleNotLinked, apiError{http.StatusNotFound, codeGoogleNotLinked, "No Google account is linked", ""}},
	{service.ErrReauthRequired, apiError{http.StatusBadRequest, codeReauthRequired, "Confirm it is you",
		"Send exactly one of current_password or google"}},
	{service.ErrIncorrectPassword, apiError{http.StatusBadRequest, codeIncorrectPassword, "Current password is incorrect", ""}},
	{service.ErrNoPassword, apiError{http.StatusConflict, codeNoPassword, "This account has no password",
		"It signs in with Google only; set a password through forgot-password"}},
	{service.ErrPasswordLocked, apiError{http.StatusTooManyRequests, codeAccountLocked, "Too many incorrect passwords",
		"Try again later, or reset your password"}},
	{service.ErrGoogleReauthStale, apiError{http.StatusUnauthorized, codeReauthStale, "Sign in with Google again",
		"The Google sign-in must be less than 5 minutes old"}},
	{service.ErrGoogleAccountMismatch, apiError{http.StatusForbidden, codeGoogleAccountNotLinked,
		"This Google account is not linked to your account", ""}},
	{service.ErrEmailUnchanged, apiError{http.StatusBadRequest, codeEmailUnchanged, "New email is your current email", ""}},
	{service.ErrEmailChangeNotPending, apiError{http.StatusConflict, codeEmailChangeNotPending,
		"No email change is waiting for a code", "Start the email change again"}},
	{service.ErrSessionNotFound, apiError{http.StatusNotFound, codeSessionNotFound, "Session not found", ""}},
	{service.ErrSuperAdminCannotClose, apiError{http.StatusConflict, codeSuperAdminCannotClose,
		"A super admin cannot delete their own account", "Have another super admin remove the super_admin role first"}},
}

// handleError maps service errors to responses. Anything unexpected is a
// 500 carrying only fallback.
func (h *AuthHandler) handleError(c *gin.Context, err error, fallback string) {
	var (
		codeErr     *service.CodeError
		cooldownErr *service.CooldownError
		quotaErr    *service.EmailQuotaError
		unverified  *service.EmailNotVerifiedError
	)
	switch {
	case errors.As(err, &codeErr):
		writeCodeError(c, codeErr)
		return
	case errors.As(err, &cooldownErr):
		response.TooManyRequests(c, codeResendCooldown, "A code was sent moments ago",
			"Wait before asking for another", cooldownErr.RetryAfter)
		return
	case errors.As(err, &quotaErr):
		response.TooManyRequests(c, codeEmailQuotaExceeded, "Too many emails requested", "Try again later", quotaErr.RetryAfter)
		return
	case errors.As(err, &unverified):
		response.Fail(c, http.StatusForbidden, codeEmailNotVerified, "Email not verified",
			"Enter the code sent to your email, or ask for a new one", dto.EmailNotVerified{Email: unverified.Email})
		return
	case errors.Is(err, service.ErrEmailNotVerified):
		response.ErrorCode(c, http.StatusForbidden, codeEmailNotVerified, "Email not verified", "")
		return
	}

	for _, known := range knownErrors {
		if errors.Is(err, known.err) {
			response.ErrorCode(c, known.status, known.code, known.message, known.detail)
			return
		}
	}
	response.InternalError(c, fallback, err)
}

// codeAttempts is the data of an invalid_code answer.
type codeAttempts struct {
	AttemptsLeft int `json:"attempts_left"`
}

// writeCodeError answers a refused emailed code.
func writeCodeError(c *gin.Context, err *service.CodeError) {
	switch {
	case errors.Is(err, service.ErrInvalidCode):
		response.Fail(c, http.StatusBadRequest, codeInvalidCode, "Invalid code", "",
			codeAttempts{AttemptsLeft: err.AttemptsLeft})
	case errors.Is(err, service.ErrTooManyAttempts):
		response.TooManyRequests(c, codeTooManyAttempts, "Too many wrong codes",
			"Ask for a new code", err.RetryAfter)
	default:
		response.ErrorCode(c, http.StatusBadRequest, codeCodeExpired, "This code has expired",
			"Ask for a new code")
	}
}
