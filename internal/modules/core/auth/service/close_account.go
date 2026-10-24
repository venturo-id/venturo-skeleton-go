package service

import (
	"context"
	"errors"
	"time"

	"timebox-be/internal/database"
	"timebox-be/internal/shared/audit"
	"timebox-be/pkg/logger"
)

// ErrSuperAdminCannotClose: a super admin must hand the role over (another
// super admin removes it) before closing their account, so the system is
// never left without one by accident.
var ErrSuperAdminCannotClose = errors.New("a super admin cannot close their own account")

// CloseAccount deletes the caller's account at their request, once they
// have proven ownership again (see reauthenticate).
//
// The users row stays — other records refer to it — but everything that
// identifies the person goes, in one transaction: email, username, name,
// phone, avatar and password (UserRepository.Anonymize), linked Google
// accounts, sessions with the devices and IP addresses they recorded,
// pending emailed codes, role assignments and API keys. Audit log entries
// stay until they expire, as a security record: they name the account by
// id only, but keep the IP address and user agent of each request. The
// person is told at the address the account had.
func (s *AuthService) CloseAccount(ctx context.Context, userID string, proof Reauth) error {
	user, err := s.activeUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.reauthenticate(ctx, user, proof); err != nil {
		return err
	}

	subject, err := s.subjects.GetSubject(ctx, userID)
	if err != nil {
		return err
	}
	if subject.SuperAdmin {
		return ErrSuperAdminCannotClose
	}

	err = database.WithTx(ctx, s.db, func(ctx context.Context) error {
		if err := s.confirmReauthTx(ctx, user); err != nil {
			return err
		}
		if err := s.userRepo.Anonymize(ctx, userID, time.Now()); err != nil {
			if errors.Is(err, database.ErrNotFound) {
				return ErrUserNotActive
			}
			return err
		}
		if err := s.userIdentityRepo.DeleteByUser(ctx, userID); err != nil {
			return err
		}
		if err := s.refreshTokenRepo.DeleteAllByUser(ctx, userID); err != nil {
			return err
		}
		if err := s.accountTokenRepo.DeleteByUser(ctx, userID); err != nil {
			return err
		}
		if err := s.roleRepo.RemoveUserRoles(ctx, userID); err != nil {
			return err
		}
		return s.apiKeys.DeleteAllForUser(ctx, userID)
	})
	if err != nil {
		return err
	}
	s.invalidateSubject(ctx, userID)

	logger.Info("Account closed by its owner", logger.String("user_id", userID))
	s.record(ctx, audit.ActionAccountClosed, userID, nil)
	if user.AvatarURL != nil && s.avatars != nil {
		// The row no longer points at it; a failure leaves an orphan file,
		// not personal data anyone can find through the account.
		if err := s.avatars.Delete(ctx, *user.AvatarURL); err != nil {
			logger.Warn("Failed to delete the photo of a closed account", logger.String("user_id", userID), logger.Err(err))
		}
	}
	_ = s.mailer.SendAccountClosedEmail(user.Email, displayName(user))
	return nil
}
