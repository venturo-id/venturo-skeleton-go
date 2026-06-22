package dto

import (
	"time"

	"venturo-skeleton-go/internal/modules/core/auth/domain"
)

// SessionResponse is one active session in the "my sessions" list
// (GET /core/v1/auth/sessions). It is a deliberately narrow view of a
// core.refresh_tokens row: the token_hash is NEVER exposed — only
// metadata the user needs to recognise and manage a device.
//
// IsCurrent marks the session whose refresh token was presented on this
// request, so the FE can label "This device" and warn before revoking it.
type SessionResponse struct {
	ID         string            `json:"id"`
	DeviceInfo domain.DeviceInfo `json:"device_info"`
	IPAddress  *string           `json:"ip_address,omitempty"`
	LastUsedAt *time.Time        `json:"last_used_at,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	IsCurrent  bool              `json:"is_current"`
}
