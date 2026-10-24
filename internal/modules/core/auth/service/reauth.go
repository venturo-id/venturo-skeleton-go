package service

import (
	"context"
	"errors"
	"time"

	userDomain "timebox-be/internal/modules/core/user/domain"
	"timebox-be/pkg/googleauth"
)

// reauthMaxAge is how recent a Google sign-in must be to stand in for the
// password.
const reauthMaxAge = 5 * time.Minute

var (
	// ErrReauthRequired: a sensitive change needs exactly one of the
	// current password or a fresh Google sign-in.
	ErrReauthRequired = errors.New("confirm with your current password or a fresh Google sign-in")
	// ErrGoogleReauthStale: the Google sign-in is older than reauthMaxAge.
	ErrGoogleReauthStale = errors.New("google sign-in is not recent enough")
	// ErrGoogleAccountMismatch: the Google account is not linked to the
	// caller's account.
	ErrGoogleAccountMismatch = errors.New("google account is not linked to this account")
)

// Reauth is the proof a sensitive change asks for: exactly one of the
// current password, or a Google sign-in made just now into the Google
// account linked to the caller's.
type Reauth struct {
	Password string
	Google   *googleauth.Exchange
}

// reauthenticate makes the caller prove again that they own the account
// before a change that would hand it, or its data, to someone else: a
// session alone — perhaps a stolen access token — is not enough.
//
// The proof is the current password, checked under the sign-in lock, or a
// Google sign-in into the Google account linked to this one (an account
// that signs in only with Google has no password).
func (s *AuthService) reauthenticate(ctx context.Context, user *userDomain.User, proof Reauth) error {
	switch {
	case proof.Password != "" && proof.Google == nil:
		attempt, err := s.checkPassword(ctx, user, proof.Password, "reauth")
		if err != nil {
			return err
		}
		cleared, err := s.userRepo.ClearPasswordAttempts(ctx, user.ID, user.PasswordHash, attempt)
		if err != nil {
			return err
		}
		if !cleared {
			// A parallel guess locked the account meanwhile, or the password
			// was reset since it was read.
			return ErrPasswordLocked
		}
		return nil
	case proof.Google != nil && proof.Password == "":
		return s.checkGoogleReauth(ctx, user, *proof.Google)
	}
	return ErrReauthRequired
}

// confirmReauthTx locks the account, in the transaction ctx carries, and
// checks that the proof reauthenticate accepted for user still stands:
// the password it checked and the email are still the account's. A
// password reset, password change or account claim that landed since
// voids the proof; otherwise a link issued now would outlive that reset.
// Call it first in the transaction that makes the change.
func (s *AuthService) confirmReauthTx(ctx context.Context, user *userDomain.User) error {
	current, err := s.userRepo.FindByIDForUpdate(ctx, user.ID)
	if err != nil {
		return err
	}
	if current == nil || !current.IsActive {
		return ErrUserNotActive
	}
	if current.PasswordHash != user.PasswordHash || current.Email != user.Email {
		return ErrReauthRequired
	}
	return nil
}
