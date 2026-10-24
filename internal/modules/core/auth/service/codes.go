package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"

	"timebox-be/internal/modules/core/auth/domain"
	"timebox-be/internal/modules/core/auth/dto"
	userDomain "timebox-be/internal/modules/core/user/domain"
	"timebox-be/pkg/jwt"
	"timebox-be/pkg/logger"
	"timebox-be/pkg/token"
)

// ─── Emailed one-time codes ─────────────────────────────────────────
//
// Email verification, password reset and email change prove control of a
// mailbox with a 6-digit code (core.account_tokens). A code works once,
// for OTP_EXPIRATION, and dies after OTP_MAX_ATTEMPTS wrong guesses;
// asking for a new one retires the previous one. Codes of one kind to one
// address are at least OTP_RESEND_COOLDOWN apart whether or not an account
// uses the address, so the answer never tells; and an account gets at most
// MAX_VERIFICATION_EMAILS / MAX_PASSWORD_RESET_EMAILS of each kind an hour,
// beyond which requests are answered as usual but send nothing.

// codeDigits is the length of an emailed code.
const codeDigits = 6

// Fallbacks for a service built without these settings. Production reads
// OTP_EXPIRATION, OTP_MAX_ATTEMPTS, OTP_RESEND_COOLDOWN and
// RESET_TOKEN_EXPIRATION.
const (
	defaultOTPExpiration     = 15 * time.Minute
	defaultOTPMaxAttempts    = 5
	defaultOTPResendCooldown = time.Minute
	defaultResetGrantTTL     = 10 * time.Minute
)

// Kinds of code, for the resend cooldown and the hourly quota.
const (
	codeKindVerify      = "verify"
	codeKindReset       = "reset"
	codeKindChangeEmail = "change-email"
)

var (
	// ErrInvalidCode: the code is not the current one — wrong, or an
	// earlier code a newer one replaced; the current one still works.
	ErrInvalidCode = errors.New("invalid code")
	// ErrCodeExpired: there is no code to check — the current one expired
	// or was used, codes were retired (a password change), or none was
	// sent.
	ErrCodeExpired = errors.New("code expired")
	// ErrTooManyAttempts: the code died of wrong guesses; ask for another.
	ErrTooManyAttempts = errors.New("too many wrong codes")
	// ErrResendCooldown: a code of this kind went out moments ago.
	ErrResendCooldown = errors.New("a code was sent moments ago")
)

// CodeError is a refused code, with what the client shows next.
type CodeError struct {
	// Err is ErrInvalidCode, ErrCodeExpired or ErrTooManyAttempts.
	Err error
	// AttemptsLeft is set with ErrInvalidCode.
	AttemptsLeft int
	// RetryAfter is set with ErrTooManyAttempts: how long until a new code
	// may be asked for (0: now).
	RetryAfter time.Duration
}

func (e *CodeError) Error() string { return e.Err.Error() }
func (e *CodeError) Unwrap() error { return e.Err }

func codeExpired() error { return &CodeError{Err: ErrCodeExpired} }

// CooldownError is ErrResendCooldown with the time left.
type CooldownError struct {
	RetryAfter time.Duration
}

func (e *CooldownError) Error() string { return ErrResendCooldown.Error() }
func (e *CooldownError) Unwrap() error { return ErrResendCooldown }

func (s *AuthService) otpTTL() time.Duration {
	if s.security.OTPExpiration <= 0 {
		return defaultOTPExpiration
	}
	return s.security.OTPExpiration
}

func (s *AuthService) otpMaxAttempts() int {
	if s.security.OTPMaxAttempts <= 0 {
		return defaultOTPMaxAttempts
	}
	return s.security.OTPMaxAttempts
}

func (s *AuthService) resendCooldown() time.Duration {
	if s.security.OTPResendCooldown <= 0 {
		return defaultOTPResendCooldown
	}
	return s.security.OTPResendCooldown
}

func (s *AuthService) resetGrantTTL() time.Duration {
	if s.security.ResetTokenExpiration <= 0 {
		return defaultResetGrantTTL
	}
	return s.security.ResetTokenExpiration
}

// resendKey names the cooldown of codes of kind to subject (an address, or
// for an email change the user id).
func resendKey(kind, subject string) string {
	return "otp:" + kind + ":" + subject
}

// holdResend starts the resend cooldown under key, or returns a
// *CooldownError while one runs. If Redis is unavailable the code goes
// out: the hourly quota and the per-IP limit still apply.
func (s *AuthService) holdResend(ctx context.Context, key string) error {
	held, remaining, err := s.limiter.Hold(ctx, key, s.resendCooldown())
	if err != nil {
		logger.Warn("Code resend cooldown unavailable, sending anyway", logger.Err(err))
		return nil
	}
	if !held {
		return &CooldownError{RetryAfter: remaining}
	}
	return nil
}

// resendRemaining is the time left on the cooldown under key.
func (s *AuthService) resendRemaining(ctx context.Context, key string) time.Duration {
	remaining, err := s.limiter.Remaining(ctx, key)
	if err != nil {
		logger.Warn("Code resend cooldown unavailable", logger.Err(err))
		return 0
	}
	return remaining
}

// ticket is the answer to a request that sent a code (or would have) and
// started the cooldown under key.
func (s *AuthService) ticket(ctx context.Context, key string) dto.CodeTicket {
	return dto.CodeTicket{
		ExpiresIn:         int(s.otpTTL().Seconds()),
		ResendAvailableIn: seconds(s.resendRemaining(ctx, key)),
	}
}

// seconds rounds d up to whole seconds.
func seconds(d time.Duration) int {
	return int((d + time.Second - 1) / time.Second)
}

// codeKey keys the code hashes (token.HashCode). It is derived from
// JWT_SECRET under a label of its own, so the two uses never share a key.
func codeKey() ([]byte, error) {
	secret, err := jwt.GetSecret()
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("timebox/account-codes"))
	return mac.Sum(nil), nil
}

// issueCode stores a new code for user and returns it; the code itself
// only goes into the email. Earlier unused codes of the purpose stop
// working. newEmail is set for PurposeChangeEmail only.
func (s *AuthService) issueCode(ctx context.Context, user *userDomain.User, purpose domain.AccountTokenPurpose, newEmail *string) (string, error) {
	key, err := codeKey()
	if err != nil {
		return "", err
	}
	code, err := token.GenerateCode(codeDigits)
	if err != nil {
		return "", err
	}

	id := uuid.New().String()
	now := time.Now()
	err = s.accountTokenRepo.Issue(ctx, &domain.AccountToken{
		ID:        id,
		UserID:    user.ID,
		Purpose:   purpose,
		TokenHash: token.HashCode(key, id, code),
		Email:     user.Email,
		NewEmail:  newEmail,
		ExpiresAt: now.Add(s.otpTTL()),
		CreatedAt: now,
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

// redeemCodeTx checks code against userID's current code for purpose, in
// the transaction ctx carries, and uses the code up when it is right.
//
// A refused code comes back as a *CodeError, and a wrong guess has been
// counted by then: the caller commits anyway (returning nil from its
// transaction) and returns the error afterwards, or the count is lost.
// Guesses are checked one at a time (the code's row is locked), so no
// more than OTP_MAX_ATTEMPTS of them are ever checked. resend names the
// cooldown of this kind of code, for when a dead code asks for a new one.
func (s *AuthService) redeemCodeTx(ctx context.Context, userID string, purpose domain.AccountTokenPurpose, code, resend string) (*domain.AccountToken, error) {
	t, err := s.accountTokenRepo.FindCurrentForUpdate(ctx, userID, purpose)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	maxAttempts := s.otpMaxAttempts()
	switch {
	case t == nil || !now.Before(t.ExpiresAt):
		return nil, codeExpired()
	case t.Attempts >= maxAttempts:
		return nil, &CodeError{Err: ErrTooManyAttempts, RetryAfter: s.resendRemaining(ctx, resend)}
	}

	key, err := codeKey()
	if err != nil {
		return nil, err
	}
	if !token.EqualHash(t.TokenHash, token.HashCode(key, t.ID, code)) {
		attempts, err := s.accountTokenRepo.CountWrongAttempt(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		logger.Warn("Wrong code", logger.String("user_id", userID),
			logger.String("purpose", string(purpose)), logger.Int("attempt", attempts))
		if attempts >= maxAttempts {
			return nil, &CodeError{Err: ErrTooManyAttempts, RetryAfter: s.resendRemaining(ctx, resend)}
		}
		return nil, &CodeError{Err: ErrInvalidCode, AttemptsLeft: maxAttempts - attempts}
	}

	if _, err := s.accountTokenRepo.MarkUsed(ctx, t.ID, now); err != nil {
		return nil, err
	}
	return t, nil
}

// refusedCode reports whether err is a refused code, which the caller's
// transaction commits before returning it (see redeemCodeTx).
func refusedCode(err error) bool {
	var codeErr *CodeError
	return errors.As(err, &codeErr)
}
