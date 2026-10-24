package service

import (
	"context"
	"errors"

	"timebox-be/internal/database"
	"timebox-be/internal/modules/core/auth/dto"
	"timebox-be/internal/shared/audit"
)

// ErrSessionNotFound: the caller has no live session with that id.
var ErrSessionNotFound = errors.New("session not found")

// ListSessions returns the caller's live sessions, most recently active
// first. currentSessionID marks the one the request was made from.
func (s *AuthService) ListSessions(ctx context.Context, userID, currentSessionID string) ([]dto.SessionResponse, error) {
	tokens, err := s.refreshTokenRepo.ListActiveByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	sessions := make([]dto.SessionResponse, len(tokens))
	for i, t := range tokens {
		sessions[i] = dto.SessionResponse{
			ID:         t.SessionID,
			Device:     t.DeviceInfo,
			IPAddress:  t.IPAddress,
			SignedInAt: t.SignedInAt,
			// The live refresh token was issued at the last refresh, which
			// an active client does every access-token lifetime.
			LastActiveAt: t.CreatedAt,
			ExpiresAt:    t.ExpiresAt,
			Current:      t.SessionID == currentSessionID,
		}
	}
	return sessions, nil
}

// EndSession signs one of the caller's sessions out — on another device,
// or this one: its refresh token is revoked and its access tokens are
// refused from the next request on.
func (s *AuthService) EndSession(ctx context.Context, userID, sessionID string) error {
	err := database.WithTx(ctx, s.db, func(ctx context.Context) error {
		// Waits for a refresh of the session in flight, then revokes the
		// token it issued (see UserRepository.FindByIDForUpdate).
		if _, err := s.userRepo.FindByIDForUpdate(ctx, userID); err != nil {
			return err
		}
		return s.refreshTokenRepo.EndSession(ctx, userID, sessionID)
	})
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return ErrSessionNotFound
		}
		return err
	}
	s.invalidateSubject(ctx, userID)
	s.record(ctx, audit.ActionSessionEnded, userID, map[string]any{"session_id": sessionID})
	return nil
}
