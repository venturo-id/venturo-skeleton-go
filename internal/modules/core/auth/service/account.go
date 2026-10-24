package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"timebox-be/internal/database"
	"timebox-be/internal/modules/core/auth/domain"
	"timebox-be/internal/modules/core/auth/dto"
	userDomain "timebox-be/internal/modules/core/user/domain"
	"timebox-be/internal/shared/audit"
	"timebox-be/pkg/crypto"
	"timebox-be/pkg/logger"
	"timebox-be/pkg/token"
	"timebox-be/pkg/validator"
)

// ─── Email verification and password reset ──────────────────────────
//
// Both prove control of the mailbox with an emailed code (see codes.go).
// Asking for a code answers the same whether or not the account exists.

// emailQuotaWindow is the period the per-account email limits count in.
const emailQuotaWindow = time.Hour

func (s *AuthService) maxVerificationEmails() int {
	if s.security.MaxVerificationEmails <= 0 {
		return defaultMaxVerificationEmails
	}
	return s.security.MaxVerificationEmails
}

func (s *AuthService) maxResetEmails() int {
	if s.security.MaxPasswordResetEmails <= 0 {
		return defaultMaxResetEmails
	}
	return s.security.MaxPasswordResetEmails
}

// displayName is how emails greet the user.
func displayName(u *userDomain.User) string {
	if u.FullName != nil && strings.TrimSpace(*u.FullName) != "" {
		return *u.FullName
	}
	return u.Username
}

// withinEmailQuota reports whether one more email of kind may go to the
// account this hour and, when not, how long until one may. If Redis is
// unavailable the email is allowed: the per-IP limit on the endpoints
// still applies.
func (s *AuthService) withinEmailQuota(ctx context.Context, kind, userID string, limit int) (bool, time.Duration) {
	allowed, retryAfter, err := s.limiter.Allow(ctx, "email:"+kind+":"+userID, limit, emailQuotaWindow)
	if err != nil {
		logger.Warn("Email quota unavailable, sending anyway", logger.String("kind", kind), logger.Err(err))
	}
	if !allowed {
		logger.Warn("Email quota reached; not sending", logger.String("kind", kind), logger.String("user_id", userID))
	}
	return allowed, retryAfter
}

// sendVerificationCode emails user a code that verifies their address,
// unless the hourly quota is used up.
func (s *AuthService) sendVerificationCode(ctx context.Context, user *userDomain.User) error {
	if allowed, _ := s.withinEmailQuota(ctx, codeKindVerify, user.ID, s.maxVerificationEmails()); !allowed {
		return nil
	}
	code, err := s.issueCode(ctx, user, domain.PurposeVerifyEmail, nil)
	if err != nil {
		return err
	}
	return s.mailer.SendVerificationCode(user.Email, displayName(user), code, s.otpTTL())
}

// ResendVerificationCode sends a new verification code to an active,
// unverified account with this email. For any other address it sends
// nothing, and answers the same: the cooldown runs per address either way.
func (s *AuthService) ResendVerificationCode(ctx context.Context, emailAddr string) (dto.CodeTicket, error) {
	emailAddr = validator.NormalizeEmail(emailAddr)
	resend := resendKey(codeKindVerify, emailAddr)
	if err := s.holdResend(ctx, resend); err != nil {
		return dto.CodeTicket{}, err
	}

	user, err := s.userRepo.FindByEmail(ctx, emailAddr)
	if err != nil {
		return dto.CodeTicket{}, err
	}
	if user != nil && user.IsActive && !user.IsEmailVerified {
		if err := s.sendVerificationCode(ctx, user); err != nil {
			return dto.CodeTicket{}, err
		}
	}
	return s.ticket(ctx, resend), nil
}

// VerifyEmail redeems a verification code and signs the account in: the
// code proves control of the mailbox, as a password would prove the
// account. The first verification of an account is welcomed by email.
func (s *AuthService) VerifyEmail(ctx context.Context, emailAddr, code string, deviceInfo domain.DeviceInfo, ipAddress string) (*dto.SignInResponse, error) {
	emailAddr = validator.NormalizeEmail(emailAddr)
	user, err := s.userRepo.FindByEmail(ctx, emailAddr)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, codeExpired()
	}

	var (
		sess          *session
		refused       error
		newlyVerified bool
	)
	// Under the user row lock, like a sign-in: a change ending every
	// session either lands first or waits and ends this one.
	err = database.WithTx(ctx, s.db, func(ctx context.Context) error {
		current, err := s.userRepo.FindByIDForUpdate(ctx, user.ID)
		if err != nil {
			return err
		}
		if current == nil || !current.IsActive {
			// Answered like an unknown address: an inactive account is never
			// sent a code, and the answer must not tell it exists.
			refused = codeExpired()
			return nil
		}

		t, err := s.redeemCodeTx(ctx, current.ID, domain.PurposeVerifyEmail, code, resendKey(codeKindVerify, emailAddr))
		if refusedCode(err) {
			refused = err
			return nil
		}
		if err != nil {
			return err
		}

		now := time.Now()
		// Fails when the account's email changed since the code was sent:
		// the code no longer applies.
		newlyVerified, err = s.userRepo.VerifyEmail(ctx, current.ID, t.Email, now)
		if errors.Is(err, database.ErrNotFound) {
			refused = codeExpired()
			return nil
		}
		if err != nil {
			return err
		}
		current.MarkEmailVerified(true, now)
		user = current

		if err := s.userRepo.TouchLastLogin(ctx, user.ID); err != nil {
			return err
		}
		sess, err = s.startSession(ctx, user, deviceInfo, ipAddress)
		return err
	})
	if err != nil {
		return nil, err
	}
	if refused != nil {
		return nil, refused
	}

	logger.Info("Email verified", logger.String("user_id", user.ID))
	s.record(ctx, audit.ActionEmailVerified, user.ID, nil)
	s.record(ctx, audit.ActionSignIn, user.ID, map[string]any{"method": "email_code", "session_id": sess.sessionID})
	if newlyVerified {
		_ = s.mailer.SendWelcomeEmail(user.Email, displayName(user))
	}
	return s.signInResponse(ctx, user, sess)
}

// ForgotPassword emails a password reset code to the active account with
// this email, unless the hourly quota is used up. For any other address it
// sends nothing, and answers the same. An account without a password
// (Google only) gets a code too, which is how such a user sets one.
func (s *AuthService) ForgotPassword(ctx context.Context, emailAddr string) (dto.CodeTicket, error) {
	emailAddr = validator.NormalizeEmail(emailAddr)
	resend := resendKey(codeKindReset, emailAddr)
	if err := s.holdResend(ctx, resend); err != nil {
		return dto.CodeTicket{}, err
	}

	user, err := s.userRepo.FindByEmail(ctx, emailAddr)
	if err != nil {
		return dto.CodeTicket{}, err
	}
	if user != nil && user.IsActive && s.withinResetQuota(ctx, user.ID) {
		code, err := s.issueCode(ctx, user, domain.PurposeResetPassword, nil)
		if err != nil {
			return dto.CodeTicket{}, err
		}
		if err := s.mailer.SendPasswordResetCode(user.Email, displayName(user), code, s.otpTTL()); err != nil {
			return dto.CodeTicket{}, err
		}
	}
	return s.ticket(ctx, resend), nil
}

// withinResetQuota reports whether one more reset code may go to userID
// this hour.
func (s *AuthService) withinResetQuota(ctx context.Context, userID string) bool {
	allowed, _ := s.withinEmailQuota(ctx, codeKindReset, userID, s.maxResetEmails())
	return allowed
}

// VerifyResetCode redeems a password reset code for a reset grant: a
// short-lived, single-use token that sets the new password
// (ResetPassword). The two steps let the app ask for the new password only
// once the code is known to be right.
func (s *AuthService) VerifyResetCode(ctx context.Context, emailAddr, code string) (*dto.ResetGrantResponse, error) {
	emailAddr = validator.NormalizeEmail(emailAddr)
	user, err := s.userRepo.FindByEmail(ctx, emailAddr)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, codeExpired()
	}

	var (
		grant   string
		refused error
	)
	err = database.WithTx(ctx, s.db, func(ctx context.Context) error {
		current, err := s.userRepo.FindByIDForUpdate(ctx, user.ID)
		if err != nil {
			return err
		}
		if current == nil || !current.IsActive {
			// Answered like an unknown address: an inactive account is never
			// sent a code, and the answer must not tell it exists.
			refused = codeExpired()
			return nil
		}

		t, err := s.redeemCodeTx(ctx, current.ID, domain.PurposeResetPassword, code, resendKey(codeKindReset, emailAddr))
		if refusedCode(err) {
			refused = err
			return nil
		}
		if err != nil {
			return err
		}
		if t.Email != current.Email {
			refused = codeExpired()
			return nil
		}

		grant, err = token.GenerateSecureToken(32)
		if err != nil {
			return err
		}
		now := time.Now()
		return s.accountTokenRepo.Issue(ctx, &domain.AccountToken{
			ID:        uuid.New().String(),
			UserID:    current.ID,
			Purpose:   domain.PurposeResetGrant,
			TokenHash: token.Hash(grant),
			Email:     t.Email,
			ExpiresAt: now.Add(s.resetGrantTTL()),
			CreatedAt: now,
		})
	})
	if err != nil {
		return nil, err
	}
	if refused != nil {
		return nil, refused
	}

	return &dto.ResetGrantResponse{ResetToken: grant, ExpiresIn: int(s.resetGrantTTL().Seconds())}, nil
}

// ResetPassword redeems a reset grant, sets newPassword and signs the
// account in on this client.
//
// The code behind the grant proved control of the mailbox, so the reset
// also verifies the email and lifts a sign-in lock. Every other session of
// the account ends. If the email was unverified until now, the account may
// have been registered by someone else with this address: its API keys are
// revoked too (see endPriorAccessTx), and the password the registrant
// chose is gone.
func (s *AuthService) ResetPassword(ctx context.Context, resetToken, newPassword string, deviceInfo domain.DeviceInfo, ipAddress string) (*dto.SignInResponse, error) {
	passwordHash, err := crypto.HashPassword(newPassword)
	if err != nil {
		logger.Error("Failed to hash password", logger.Err(err))
		return nil, err
	}

	var (
		user    *userDomain.User
		sess    *session
		claimed bool
	)
	// The new password, the end of every earlier session and (on a claim)
	// API key, and the new session commit together.
	err = database.WithTx(ctx, s.db, func(ctx context.Context) error {
		t, err := s.accountTokenRepo.Consume(ctx, domain.PurposeResetGrant, token.Hash(resetToken))
		if err != nil {
			return err
		}
		if t == nil {
			return ErrInvalidResetToken
		}

		u, err := s.userRepo.FindByIDForUpdate(ctx, t.UserID)
		if err != nil {
			return err
		}
		if u == nil || !u.IsActive {
			return ErrUserNotActive
		}
		if u.Email != t.Email {
			return ErrInvalidResetToken
		}

		now := time.Now()
		if err := s.userRepo.ResetPassword(ctx, u.ID, t.Email, passwordHash, now); err != nil {
			if errors.Is(err, database.ErrNotFound) {
				return ErrInvalidResetToken
			}
			return err
		}
		claimed = !u.IsEmailVerified
		if err := s.endPriorAccessTx(ctx, u.ID, claimed); err != nil {
			return err
		}

		// Issued after the revocation, so it outlives it.
		if user, err = s.userRepo.FindByID(ctx, u.ID); err != nil {
			return err
		}
		if err := s.userRepo.TouchLastLogin(ctx, user.ID); err != nil {
			return err
		}
		sess, err = s.startSession(ctx, user, deviceInfo, ipAddress)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.invalidateSubject(ctx, user.ID)

	logger.Info("Password reset", logger.String("user_id", user.ID), logger.Bool("claimed", claimed))
	s.record(ctx, audit.ActionPasswordReset, user.ID, map[string]any{"claimed": claimed})
	if claimed {
		s.record(ctx, audit.ActionAccountClaimed, user.ID, map[string]any{"via": "password_reset"})
	}
	s.record(ctx, audit.ActionSignIn, user.ID, map[string]any{"method": "password_reset", "session_id": sess.sessionID})
	_ = s.mailer.SendPasswordChangedEmail(user.Email, displayName(user))
	return s.signInResponse(ctx, user, sess)
}

// endPriorAccessTx ends, in the transaction ctx carries, whatever access
// to an account existed before its owner just proved control of its
// email, or changed its password. Sessions always end, and so do the
// emailed codes not used yet: an email change asked for by whoever held
// the account must not hand it back to them. claimed means the email had
// never been verified, so the previous holder may have been someone else
// who registered with this address: the API keys they could have created
// are revoked as well. The caller invalidates the authz cache after
// commit.
func (s *AuthService) endPriorAccessTx(ctx context.Context, userID string, claimed bool) error {
	if err := s.revokeSessionsTx(ctx, userID); err != nil {
		return err
	}
	if err := s.accountTokenRepo.RetireAll(ctx, userID, time.Now()); err != nil {
		return err
	}
	if claimed {
		return s.apiKeys.RevokeAllForUser(ctx, userID, "account claimed by the verified owner of its email")
	}
	return nil
}

// ─── Password change ────────────────────────────────────────────────

// ChangePassword replaces the signed-in user's password.
//
// A session alone — a stolen access token, a device left signed in — must
// not allow unlimited guesses at the password: the current password is
// checked under the same lock as sign-in (see reservePasswordAttempt), the
// owner is emailed when it locks, and a locked account refuses even the
// right one.
//
// The new password and the end of every session, this one included, and
// of every emailed code not used yet (see endPriorAccessTx) commit
// together.
func (s *AuthService) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil || !user.IsActive {
		return ErrUserNotActive
	}

	attempt, err := s.checkPassword(ctx, user, currentPassword, "password_change")
	if err != nil {
		return err
	}

	passwordHash, err := crypto.HashPassword(newPassword)
	if err != nil {
		logger.Error("Failed to hash password", logger.Err(err))
		return err
	}

	err = database.WithTx(ctx, s.db, func(ctx context.Context) error {
		// Refused when a parallel guess locked the account since this
		// attempt was counted, or the password was reset meanwhile.
		changed, err := s.userRepo.ChangePassword(ctx, userID, user.PasswordHash, passwordHash, attempt, time.Now())
		if err != nil {
			return err
		}
		if !changed {
			if now, err := s.userRepo.FindByID(ctx, userID); err == nil && now != nil && now.PasswordHash != user.PasswordHash {
				return ErrIncorrectPassword
			}
			return ErrPasswordLocked
		}
		return s.endPriorAccessTx(ctx, userID, false)
	})
	if err != nil {
		return err
	}
	s.invalidateSubject(ctx, userID)

	logger.Info("Password changed", logger.String("user_id", userID))
	s.record(ctx, audit.ActionPasswordChanged, userID, nil)
	_ = s.mailer.SendPasswordChangedEmail(user.Email, displayName(user))
	return nil
}

// checkPassword checks password as user's current one under the sign-in
// lock (see reservePasswordAttempt) and returns the attempt it counted as;
// the caller ends the run of wrong passwords once it acted on a right one.
// purpose names the check in the audit log.
func (s *AuthService) checkPassword(ctx context.Context, user *userDomain.User, password, purpose string) (int, error) {
	if user.PasswordHash == "" {
		return 0, ErrNoPassword
	}
	attempt, err := s.reservePasswordAttempt(ctx, user.ID)
	if err != nil {
		return 0, err
	}
	if attempt == 0 {
		return 0, ErrPasswordLocked
	}
	if !crypto.ComparePassword(user.PasswordHash, password) {
		s.recordWrongPassword(ctx, user, attempt, purpose)
		return 0, ErrIncorrectPassword
	}
	return attempt, nil
}
