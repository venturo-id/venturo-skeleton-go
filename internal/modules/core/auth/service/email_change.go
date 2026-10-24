package service

import (
	"context"
	"errors"
	"time"

	"timebox-be/internal/database"
	"timebox-be/internal/modules/core/auth/domain"
	"timebox-be/internal/modules/core/auth/dto"
	userDomain "timebox-be/internal/modules/core/user/domain"
	userRepo "timebox-be/internal/modules/core/user/repository"
	"timebox-be/internal/shared/audit"
	"timebox-be/pkg/logger"
	"timebox-be/pkg/validator"
)

var (
	// ErrEmailUnchanged: the new address is the account's current one.
	ErrEmailUnchanged = errors.New("new email is the current email")
	// ErrEmailQuotaExceeded: the account asked for too many emails of this
	// kind this hour.
	ErrEmailQuotaExceeded = errors.New("too many emails requested")
	// ErrEmailChangeNotPending: there is no email change whose code could
	// be sent again.
	ErrEmailChangeNotPending = errors.New("no email change is pending")
)

// EmailQuotaError is ErrEmailQuotaExceeded with the time until another
// email may be asked for.
type EmailQuotaError struct {
	RetryAfter time.Duration
}

func (e *EmailQuotaError) Error() string { return ErrEmailQuotaExceeded.Error() }
func (e *EmailQuotaError) Unwrap() error { return ErrEmailQuotaExceeded }

// activeUser loads the signed-in user, refusing one deactivated or deleted
// since the access token was issued.
func (s *AuthService) activeUser(ctx context.Context, userID string) (*userDomain.User, error) {
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.IsActive {
		return nil, ErrUserNotActive
	}
	return user, nil
}

// RequestEmailChange starts moving the caller's account to newEmail. Once
// the caller has proven ownership again (see reauthenticate), a code goes
// to newEmail; the account keeps its address until the code is entered
// (ConfirmEmailChange), which proves newEmail belongs to the caller too.
// A new request replaces an earlier pending one.
func (s *AuthService) RequestEmailChange(ctx context.Context, userID, newEmail string, proof Reauth) (*dto.EmailChangeSent, error) {
	user, err := s.activeUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	newEmail = validator.NormalizeEmail(newEmail)
	if newEmail == user.Email {
		return nil, ErrEmailUnchanged
	}
	// Checked before the proof, which a Google sign-in cannot give twice.
	resend := resendKey(codeKindChangeEmail, user.ID)
	if remaining := s.resendRemaining(ctx, resend); remaining > 0 {
		return nil, &CooldownError{RetryAfter: remaining}
	}
	if err := s.reauthenticate(ctx, user, proof); err != nil {
		return nil, err
	}

	// Checked again when the change is applied; this only spares the
	// owner a code that cannot work.
	taken, err := s.userRepo.FindByEmail(ctx, newEmail)
	if err != nil {
		return nil, err
	}
	if taken != nil {
		return nil, ErrEmailAlreadyExists
	}
	_, err = s.sendEmailChangeCode(ctx, user, func(ctx context.Context) (string, error) {
		return newEmail, s.confirmReauthTx(ctx, user)
	})
	if err != nil {
		return nil, err
	}

	logger.Info("Email change requested", logger.String("user_id", user.ID))
	s.record(ctx, audit.ActionEmailChangeRequested, user.ID, nil)
	return &dto.EmailChangeSent{NewEmail: newEmail, CodeTicket: s.ticket(ctx, resend)}, nil
}

// ResendEmailChange sends a new code for the caller's pending email
// change, without asking for the proof again: the change was asked for
// with it. Pending means its code has not expired or been used; one that
// died of wrong guesses may be sent again — it goes to the address the
// caller chose, so guessing it is not what keeps the account safe.
func (s *AuthService) ResendEmailChange(ctx context.Context, userID string) (*dto.EmailChangeSent, error) {
	user, err := s.activeUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	newEmail, err := s.sendEmailChangeCode(ctx, user, func(ctx context.Context) (string, error) {
		t, err := s.accountTokenRepo.FindCurrentForUpdate(ctx, user.ID, domain.PurposeChangeEmail)
		if err != nil {
			return "", err
		}
		if t == nil || t.NewEmail == nil || !time.Now().Before(t.ExpiresAt) || t.Email != user.Email {
			return "", ErrEmailChangeNotPending
		}
		return *t.NewEmail, nil
	})
	if err != nil {
		return nil, err
	}

	return &dto.EmailChangeSent{NewEmail: newEmail, CodeTicket: s.ticket(ctx, resendKey(codeKindChangeEmail, user.ID))}, nil
}

// sendEmailChangeCode issues and sends a code moving user to the address
// target returns, within the resend cooldown and the hourly quota. target
// runs first in the transaction that issues the code, under the user
// row's lock, and may refuse; a refusal starts no cooldown and uses no
// quota.
func (s *AuthService) sendEmailChangeCode(ctx context.Context, user *userDomain.User, target func(ctx context.Context) (string, error)) (string, error) {
	var newEmail, code string
	err := database.WithTx(ctx, s.db, func(ctx context.Context) error {
		if _, err := s.userRepo.FindByIDForUpdate(ctx, user.ID); err != nil {
			return err
		}
		var err error
		if newEmail, err = target(ctx); err != nil {
			return err
		}
		if err := s.holdResend(ctx, resendKey(codeKindChangeEmail, user.ID)); err != nil {
			return err
		}
		if allowed, retryAfter := s.withinEmailQuota(ctx, codeKindChangeEmail, user.ID, s.maxVerificationEmails()); !allowed {
			return &EmailQuotaError{RetryAfter: retryAfter}
		}
		code, err = s.issueCode(ctx, user, domain.PurposeChangeEmail, &newEmail)
		return err
	})
	if err != nil {
		return "", err
	}
	return newEmail, s.mailer.SendEmailChangeCode(newEmail, displayName(user), code, s.otpTTL())
}

// ConfirmEmailChange redeems the caller's email change code: the account
// moves to the new address, which counts as verified — the code was sent
// there — and the old address is told. Codes sent to the old address
// (verification, password reset) stop working with the move.
func (s *AuthService) ConfirmEmailChange(ctx context.Context, userID, code string) (*dto.UserInfo, error) {
	var (
		user     *userDomain.User
		oldEmail string
		refused  error
	)
	err := database.WithTx(ctx, s.db, func(ctx context.Context) error {
		current, err := s.userRepo.FindByIDForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		if current == nil || !current.IsActive {
			return ErrUserNotActive
		}

		t, err := s.redeemCodeTx(ctx, userID, domain.PurposeChangeEmail, code, resendKey(codeKindChangeEmail, userID))
		if refusedCode(err) {
			refused = err
			return nil
		}
		if err != nil {
			return err
		}
		if t.NewEmail == nil || current.Email != t.Email {
			refused = codeExpired()
			return nil
		}

		// Taken meanwhile: the transaction rolls back and the code still
		// works, should the address become free again.
		now := time.Now()
		err = s.userRepo.ChangeEmail(ctx, userID, t.Email, *t.NewEmail, now)
		switch {
		case errors.Is(err, database.ErrNotFound):
			refused = codeExpired()
			return nil
		case errors.Is(err, userRepo.ErrEmailTaken):
			return ErrEmailAlreadyExists
		case err != nil:
			return err
		}
		oldEmail = current.Email
		current.Email = *t.NewEmail
		current.MarkEmailVerified(true, now)
		user = current
		return nil
	})
	if err != nil {
		return nil, err
	}
	if refused != nil {
		return nil, refused
	}

	logger.Info("Email changed", logger.String("user_id", user.ID))
	s.record(ctx, audit.ActionEmailChanged, user.ID, nil)
	_ = s.mailer.SendEmailChangedEmail(oldEmail, displayName(user), user.Email)

	info, err := s.userInfo(ctx, user)
	if err != nil {
		return nil, err
	}
	return &info, nil
}
