package dto

// SignInRequest represents the signin request payload
type SignInRequest struct {
	Login    string `json:"login" binding:"required"`
	Password string `json:"password" binding:"required,min=8"`
}

// SignInResponse represents the signin response
type SignInResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    int      `json:"expires_in"`
	User         UserInfo `json:"user"`
	Roles        []string `json:"roles"`
	Permissions  []string `json:"permissions"`
}

// UserInfo represents basic user info in auth response
type UserInfo struct {
	ID       string  `json:"id"`
	Email    string  `json:"email"`
	Username string  `json:"username"`
	FullName *string `json:"full_name,omitempty"`
}
