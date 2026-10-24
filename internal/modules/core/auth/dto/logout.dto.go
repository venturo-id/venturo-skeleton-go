package dto

// LogoutRequest represents the logout request payload. The refresh token
// may instead come from the web client's cookie.
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"omitempty,max=128"`
}
