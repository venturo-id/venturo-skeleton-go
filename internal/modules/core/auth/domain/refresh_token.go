package domain

import "time"

// DeviceInfo represents device information stored as JSONB
type DeviceInfo struct {
	Name    string `json:"name,omitempty"`
	Type    string `json:"type,omitempty"`
	OS      string `json:"os,omitempty"`
	Browser string `json:"browser,omitempty"`
}

// RefreshToken represents a refresh token entity
type RefreshToken struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	TokenHash  string     `json:"-"`
	DeviceInfo DeviceInfo `json:"device_info"`
	IPAddress  *string    `json:"ip_address,omitempty"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	// RotatedFromID is the token this one replaced on refresh; nil for a
	// token issued at sign-in.
	RotatedFromID *string `json:"-"`
	// SessionID and SignedInAt describe the sign-in session; rotation
	// carries them over to the next token.
	SessionID  string    `json:"session_id"`
	SignedInAt time.Time `json:"signed_in_at"`
}

// IsRevoked checks if the token is revoked
func (r *RefreshToken) IsRevoked() bool {
	return r.RevokedAt != nil
}
