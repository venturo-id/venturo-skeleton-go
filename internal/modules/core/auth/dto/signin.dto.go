package dto

// SignInRequest represents the signin request payload
type SignInRequest struct {
	Login    string `json:"login" binding:"required,max=254"`
	Password string `json:"password" binding:"required,max=72"`
}

// SignInResponse is a new session: what sign-in, Google sign-in, email
// verification and password reset answer with. The web client gets the
// refresh token as a cookie instead.
type SignInResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    int      `json:"expires_in"`
	User         UserInfo `json:"user"`
	Roles        []string `json:"roles"`
	Permissions  []string `json:"permissions"`
}

// UserInfo represents basic user info in auth response
type UserInfo struct {
	ID        string  `json:"id"`
	Email     string  `json:"email"`
	Username  string  `json:"username"`
	FullName  *string `json:"full_name"`
	AvatarURL *string `json:"avatar_url"`
	Timezone  string  `json:"timezone"`
	// HasPassword is false for an account that signs in with Google only:
	// it confirms sensitive changes with a Google sign-in, and sets a
	// password through forgot-password.
	HasPassword     bool `json:"has_password"`
	IsEmailVerified bool `json:"is_email_verified"`
	// Google is the linked Google account; nil when there is none.
	Google *LinkedGoogle `json:"google"`
}

// EmailNotVerified is the data of a sign-in refused because the email is
// not verified: the address to send the code to, which the client may not
// know when the user signed in with their username.
type EmailNotVerified struct {
	Email string `json:"email"`
}
