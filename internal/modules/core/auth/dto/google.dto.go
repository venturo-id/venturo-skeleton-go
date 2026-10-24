package dto

import (
	"time"

	"timebox-be/pkg/googleauth"
)

// GoogleClientQuery is the query of GET /auth/google/client.
type GoogleClientQuery struct {
	Platform string `form:"platform" binding:"required,oneof=desktop web"`
}

// GoogleClientResponse is what the app starts Google's authorization code
// flow with. RedirectURIs are the web client's allowed redirect URIs; nil
// for desktop, which redirects to a loopback port of its choice.
type GoogleClientResponse struct {
	Platform     string   `json:"platform"`
	ClientID     string   `json:"client_id"`
	RedirectURIs []string `json:"redirect_uris"`
}

// GoogleCode is what the app got back from Google's authorization code
// flow with PKCE: the backend exchanges it for the Google account.
type GoogleCode struct {
	Code         string `json:"code" binding:"required,max=2048"`
	CodeVerifier string `json:"code_verifier" binding:"required,min=43,max=128"`
	RedirectURI  string `json:"redirect_uri" binding:"required,max=2048"`
	Platform     string `json:"platform" binding:"required,oneof=desktop web"`
}

// Exchange is the code as googleauth takes it.
func (g GoogleCode) Exchange() googleauth.Exchange {
	return googleauth.Exchange{
		Platform:     g.Platform,
		Code:         g.Code,
		CodeVerifier: g.CodeVerifier,
		RedirectURI:  g.RedirectURI,
	}
}

// GoogleSignInRequest is the body of POST /auth/google.
type GoogleSignInRequest struct {
	GoogleCode
	// Timezone is the IANA zone of a new account (see SignUpRequest); an
	// existing account keeps its own.
	Timezone *string `json:"timezone" binding:"omitempty,timezone"`
}

// GoogleSignInResponse is SignInResponse plus is_new_user, so the app
// knows when to show onboarding.
type GoogleSignInResponse struct {
	SignInResponse
	IsNewUser bool `json:"is_new_user"`
}

// LinkGoogleRequest is the body of POST /account/google/link. The current
// password is required: a session alone must not be able to add a way in.
type LinkGoogleRequest struct {
	CurrentPassword string `json:"current_password" binding:"required,max=72"`
	GoogleCode
}

// LinkedGoogle is the Google account that signs in to an account.
type LinkedGoogle struct {
	Email    *string   `json:"email"`
	LinkedAt time.Time `json:"linked_at"`
}
