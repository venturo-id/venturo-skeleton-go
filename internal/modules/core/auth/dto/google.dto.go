package dto

// GoogleSignInRequest is the body for POST /core/v1/auth/google.
//
// `id_token` is the Firebase ID token the FE obtains via:
//
//	const result = await signInWithPopup(auth, new GoogleAuthProvider())
//	const idToken  = await result.user.getIdToken()
//
// The backend verifies the token, derives a stable identity from it,
// and either signs the existing user in or provisions a brand-new user.
type GoogleSignInRequest struct {
	IDToken string `json:"id_token" binding:"required"`
}

// GoogleSignInResponse mirrors SignInResponse with one extra field —
// `is_new_user` — so the FE knows when to show its "welcome,
// onboarding…" UI vs. a normal sign-in. The token / user / roles /
// permissions shape is identical to /signin, which lets the FE reuse
// its existing post-login state handlers.
type GoogleSignInResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    int      `json:"expires_in"`
	IsNewUser    bool     `json:"is_new_user"`
	User         UserInfo `json:"user"`
	Roles        []string `json:"roles"`
	Permissions  []string `json:"permissions"`
}
