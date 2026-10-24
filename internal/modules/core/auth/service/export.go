package service

import (
	"context"
	"time"

	"timebox-be/internal/modules/core/auth/dto"
)

// exportActivityLimit is how many of the owner's own audit log entries an
// export carries, newest first.
const exportActivityLimit = 1000

// ExportAccount returns everything the account holds about its owner: the
// profile, roles, linked sign-in accounts, sessions, API keys (never their
// secrets, which are not stored) and recent activity from the audit log.
// currentSessionID marks the session the request came from.
//
// A module that stores personal data of its own adds it here, and erases
// it in CloseAccount.
func (s *AuthService) ExportAccount(ctx context.Context, userID, currentSessionID string) (*dto.AccountExport, error) {
	user, err := s.activeUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	roles, err := s.roleRepo.GetUserRoleNames(ctx, userID)
	if err != nil {
		return nil, err
	}
	if roles == nil {
		roles = []string{}
	}

	identities, err := s.userIdentityRepo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	linked := make([]dto.ExportLinkedAccount, len(identities))
	for i, id := range identities {
		linked[i] = dto.ExportLinkedAccount{
			Provider: id.Provider, Email: id.Email, LinkedAt: id.CreatedAt, LastLoginAt: id.LastLoginAt,
		}
	}

	sessions, err := s.ListSessions(ctx, userID, currentSessionID)
	if err != nil {
		return nil, err
	}
	apiKeys, err := s.apiKeys.ListAllForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	activity, err := s.audit.ListByActor(ctx, userID, exportActivityLimit)
	if err != nil {
		return nil, err
	}

	return &dto.AccountExport{
		ExportedAt: time.Now().UTC(),
		Profile: dto.ExportProfile{
			ID:              user.ID,
			Email:           user.Email,
			Username:        user.Username,
			FullName:        user.FullName,
			Phone:           user.Phone,
			AvatarURL:       user.AvatarURL,
			Timezone:        user.Timezone,
			HasPassword:     user.PasswordHash != "",
			IsEmailVerified: user.IsEmailVerified,
			EmailVerifiedAt: user.EmailVerifiedAt,
			LastLoginAt:     user.LastLoginAt,
			CreatedAt:       user.CreatedAt,
			UpdatedAt:       user.UpdatedAt,
		},
		Roles:          roles,
		LinkedAccounts: linked,
		Sessions:       sessions,
		APIKeys:        apiKeys,
		Activity:       activity,
	}, nil
}
