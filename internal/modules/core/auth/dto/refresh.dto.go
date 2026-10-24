package dto

// RefreshTokenRequest represents the refresh token request payload. The
// refresh token may instead come from the web client's cookie.
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"omitempty,max=128"`
}

// RefreshTokenResponse represents the refresh token response. The web
// client gets the refresh token as a cookie instead.
type RefreshTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}
