package service

// Integration tests for Google sign-in and for linking and unlinking the
// Google account of an account. Google is faked (fakeGoogle): what is
// tested is what the service does with the verified account. See
// account_integration_test.go for how to run them.

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"timebox-be/internal/shared/audit"
	"timebox-be/pkg/googleauth"
)

// A Google account no account knows creates one: verified, without a
// password or a role, its photo copied; signing in again finds it by the
// Google account id, whatever its email is by then.
func TestIntegration_GoogleSignUp(t *testing.T) {
	h := newHarness(t)
	local := "it-" + uuid.NewString()[:12]
	address := local + "@timebox.test"
	google := googleAccount(address)
	google.Name = "Ada Lovelace"
	google.Picture = "https://lh3.googleusercontent.com/a/ada=s96-c"
	h.google.identity = google

	resp, err := h.signInWithGoogle(t)
	if err != nil {
		t.Fatalf("SignInWithGoogle() error = %v", err)
	}
	user := resp.User
	if !resp.IsNewUser {
		t.Error("is_new_user = false for a new account")
	}
	if user.Email != address || user.Username != local || user.FullName == nil || *user.FullName != google.Name {
		t.Errorf("user = %+v, want %s named %q with username %s", user, address, google.Name, local)
	}
	if user.HasPassword || !user.IsEmailVerified {
		t.Errorf("has_password = %v, verified = %v; want no password, verified", user.HasPassword, user.IsEmailVerified)
	}
	if user.Google == nil || user.Google.Email == nil || *user.Google.Email != address {
		t.Errorf("google = %+v, want %s linked", user.Google, address)
	}
	if user.AvatarURL == nil || h.avatars.imported[*user.AvatarURL] != google.Picture {
		t.Errorf("avatar_url = %v, want a copy of %s", user.AvatarURL, google.Picture)
	}
	if len(resp.Roles) != 0 {
		t.Errorf("roles = %v, want none", resp.Roles)
	}
	h.assertSessionLive(t, user.ID, resp.AccessToken, resp.RefreshToken)
	if h.mail.welcome[address] != 1 || h.mail.verify[address] != "" || h.mail.linked[address] != "" {
		t.Errorf("emails: welcome = %d, code = %q, linked = %q; want one welcome only",
			h.mail.welcome[address], h.mail.verify[address], h.mail.linked[address])
	}

	// The Google account's address changed, and Google no longer vouches
	// for it: the link by id still holds, and the account keeps its own.
	moved := randomEmail()
	google.Email, google.EmailVerified = moved, false
	again, err := h.signInWithGoogle(t)
	if err != nil {
		t.Fatalf("second SignInWithGoogle() error = %v", err)
	}
	if again.IsNewUser || again.User.ID != user.ID || again.User.Email != address {
		t.Errorf("second sign-in got %s %s (new=%v), want the account %s at %s", again.User.ID, again.User.Email, again.IsNewUser, user.ID, address)
	}
	if again.User.Google == nil || again.User.Google.Email == nil || *again.User.Google.Email != moved {
		t.Errorf("google = %+v, want its address updated to %s", again.User.Google, moved)
	}
	if h.mail.welcome[address] != 1 {
		t.Errorf("welcome emails = %d, want 1", h.mail.welcome[address])
	}

	actions := h.audit.actions(user.ID)
	if n := countOf(actions, audit.ActionSignIn); n != 2 {
		t.Errorf("audit log %v: %d sign-ins, want 2", actions, n)
	}
}

// A Google account whose verified address is that of a verified account
// links to it: the owner keeps their password, sessions and API keys,
// and the address is told.
func TestIntegration_GoogleLinksByEmail(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	h.verify(t, address)
	session, err := h.signIn(address, "Timebox2026x")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	keyID := h.createAPIKey(t, userID)

	google := googleAccount(address)
	google.Picture = "https://lh3.googleusercontent.com/a/owner=s96-c"
	h.google.identity = google
	resp, err := h.signInWithGoogle(t)
	if err != nil {
		t.Fatalf("SignInWithGoogle() error = %v", err)
	}
	if resp.IsNewUser || resp.User.ID != userID {
		t.Fatalf("Google sign-in got %s (new=%v), want the existing %s", resp.User.ID, resp.IsNewUser, userID)
	}
	if !resp.User.HasPassword || resp.User.Google == nil {
		t.Errorf("user = %+v, want the password kept and Google linked", resp.User)
	}
	if resp.User.AvatarURL == nil || h.avatars.imported[*resp.User.AvatarURL] != google.Picture {
		t.Errorf("avatar_url = %v, want a copy of the Google photo", resp.User.AvatarURL)
	}
	if h.mail.linked[address] != address {
		t.Errorf("the address was told %q was linked, want %q", h.mail.linked[address], address)
	}

	// Nothing of the owner's access ends.
	h.assertSessionLive(t, userID, session.AccessToken, session.RefreshToken)
	if key, err := h.apiKeys.FindByID(h.ctx, keyID, userID); err != nil || key == nil || key.IsRevoked() {
		t.Errorf("API key = %+v (err %v), want it alive", key, err)
	}
	if _, err := h.signIn(address, "Timebox2026x"); err != nil {
		t.Errorf("password after linking: err = %v", err)
	}
	actions := h.audit.actions(userID)
	if !slices.Contains(actions, audit.ActionGoogleLinked) || slices.Contains(actions, audit.ActionAccountClaimed) {
		t.Errorf("audit log %v: want %s and no claim", actions, audit.ActionGoogleLinked)
	}
}

// Only an address Google verified links or creates an account.
func TestIntegration_GoogleUnverifiedEmail(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	h.verify(t, address)

	for _, target := range []string{address, randomEmail()} {
		google := googleAccount(target)
		google.EmailVerified = false
		h.google.identity = google
		if _, err := h.signInWithGoogle(t); !errors.Is(err, ErrGoogleEmailNotVerified) {
			t.Errorf("unverified %s: err = %v, want ErrGoogleEmailNotVerified", target, err)
		}
	}
	if n := h.count(t, "user_identities", userID); n != 0 {
		t.Errorf("the account has %d Google accounts linked, want 0", n)
	}
	if user, err := h.users.FindByEmail(h.ctx, h.google.identity.Email); err != nil || user != nil {
		t.Errorf("account created for an unverified Google address: %+v (err %v)", user, err)
	}
}

// An account links one Google account: another one with the same address
// cannot sign in to it.
func TestIntegration_GoogleAccountConflict(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	h.google.identity = googleAccount(address)
	first, err := h.signInWithGoogle(t)
	if err != nil {
		t.Fatalf("SignInWithGoogle() error = %v", err)
	}

	h.google.identity = googleAccount(address)
	if _, err := h.signInWithGoogle(t); !errors.Is(err, ErrGoogleAccountConflict) {
		t.Errorf("second Google account with the address: err = %v, want ErrGoogleAccountConflict", err)
	}
	if n := h.count(t, "user_identities", first.User.ID); n != 1 {
		t.Errorf("%d Google accounts linked, want 1", n)
	}
}

// What Google's answer can go wrong with, as the client is told it.
func TestIntegration_GoogleExchangeFails(t *testing.T) {
	h := newHarness(t)
	noEmail := googleAccount("")
	for _, tc := range []struct {
		name     string
		identity *googleauth.Identity
		err      error
		want     error
	}{
		{"code refused", nil, errors.New("oauth2: invalid_grant"), ErrGoogleAuthFailed},
		{"ID token does not verify", nil, googleauth.ErrInvalidIDToken, ErrGoogleAuthFailed},
		{"redirect URI not allowed", nil, googleauth.ErrInvalidRedirectURI, ErrInvalidRedirectURI},
		{"platform not configured", nil, googleauth.ErrNotConfigured, ErrGoogleNotConfigured},
		{"no email", noEmail, nil, ErrGoogleAuthFailed},
	} {
		h.google.identity, h.google.err = tc.identity, tc.err
		if _, err := h.signInWithGoogle(t); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	client, err := h.svc.GoogleClient(googleauth.PlatformWeb)
	if err != nil || client.ClientID == "" || client.Platform != googleauth.PlatformWeb {
		t.Errorf("GoogleClient() = %+v, %v", client, err)
	}
	h.svc.google = nil
	if _, err := h.svc.GoogleClient(googleauth.PlatformWeb); !errors.Is(err, ErrGoogleNotConfigured) {
		t.Errorf("GoogleClient() without Google: err = %v, want ErrGoogleNotConfigured", err)
	}
	if _, err := h.signInWithGoogle(t); !errors.Is(err, ErrGoogleNotConfigured) {
		t.Errorf("SignInWithGoogle() without Google: err = %v, want ErrGoogleNotConfigured", err)
	}
}

// Linking a Google account takes the current password; it signs in to
// the account from then on. An account links one, and a Google account
// signs in to one account.
func TestIntegration_LinkGoogle(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	photo := "https://cdn.timebox.test/avatars/" + userID + "/own.jpg"
	if _, err := h.pool.Exec(h.ctx, `UPDATE core.users SET avatar_url = $2 WHERE id = $1`, userID, photo); err != nil {
		t.Fatalf("set avatar: %v", err)
	}
	googleEmail := randomEmail()
	google := googleAccount(googleEmail)
	google.Picture = "https://lh3.googleusercontent.com/a/other=s96-c"
	h.google.identity = google
	exchange := googleCode.Exchange()

	for _, tc := range []struct {
		name, password string
		want           error
	}{
		{"no password", "", ErrReauthRequired},
		{"wrong password", "Wrongpass2026", ErrIncorrectPassword},
	} {
		if _, err := h.svc.LinkGoogle(h.ctx, userID, tc.password, exchange); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	info, err := h.svc.LinkGoogle(h.ctx, userID, "Timebox2026x", exchange)
	if err != nil {
		t.Fatalf("LinkGoogle() error = %v", err)
	}
	if info.Email != address || info.Google == nil || info.Google.Email == nil || *info.Google.Email != googleEmail {
		t.Errorf("LinkGoogle() = %+v, want %s linked to %s", info, googleEmail, address)
	}
	if info.AvatarURL == nil || *info.AvatarURL != photo || len(h.avatars.imported) != 0 {
		t.Errorf("avatar_url = %v, want the account's own photo kept", info.AvatarURL)
	}
	if h.mail.linked[address] != googleEmail {
		t.Errorf("the address was told %q was linked, want %q", h.mail.linked[address], googleEmail)
	}
	resp, err := h.signInWithGoogle(t)
	if err != nil || resp.User.ID != userID || resp.IsNewUser {
		t.Fatalf("Google sign-in after linking = %+v, %v; want the account %s", resp, err, userID)
	}

	h.google.identity = googleAccount(randomEmail())
	if _, err := h.svc.LinkGoogle(h.ctx, userID, "Timebox2026x", exchange); !errors.Is(err, ErrGoogleAlreadyLinked) {
		t.Errorf("second Google account: err = %v, want ErrGoogleAlreadyLinked", err)
	}

	otherAddress := randomEmail()
	otherID := h.signUp(t, otherAddress, "Timebox2026x")
	h.google.identity = google
	if _, err := h.svc.LinkGoogle(h.ctx, otherID, "Timebox2026x", exchange); !errors.Is(err, ErrGoogleAccountInUse) {
		t.Errorf("Google account of another account: err = %v, want ErrGoogleAccountInUse", err)
	}
	if h.mail.linked[otherAddress] != "" {
		t.Errorf("a refused link was announced to %s", otherAddress)
	}

	// An account that signs in with Google only has one linked already,
	// and no password to confirm with.
	h.google.identity = googleAccount(randomEmail())
	googleOnly, err := h.signInWithGoogle(t)
	if err != nil {
		t.Fatalf("SignInWithGoogle() error = %v", err)
	}
	if _, err := h.svc.LinkGoogle(h.ctx, googleOnly.User.ID, "Anything2026x", exchange); !errors.Is(err, ErrNoPassword) {
		t.Errorf("Google-only account: err = %v, want ErrNoPassword", err)
	}

	if !slices.Contains(h.audit.actions(userID), audit.ActionGoogleLinked) {
		t.Errorf("audit log %v lacks %s", h.audit.actions(userID), audit.ActionGoogleLinked)
	}
}

// Unlinking leaves an account with its password; one without must set a
// password first, through forgot-password.
func TestIntegration_UnlinkGoogle(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	h.google.identity = googleAccount(address)
	resp, err := h.signInWithGoogle(t)
	if err != nil {
		t.Fatalf("SignInWithGoogle() error = %v", err)
	}
	userID := resp.User.ID

	if _, err := h.svc.UnlinkGoogle(h.ctx, userID); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("Google-only account: err = %v, want ErrNoPassword", err)
	}

	if session := h.reset(t, address, "Timebox2026x"); !session.User.HasPassword || session.User.Google == nil {
		t.Fatalf("after setting a password: %+v, want a password and Google linked", session.User)
	}
	info, err := h.svc.UnlinkGoogle(h.ctx, userID)
	if err != nil {
		t.Fatalf("UnlinkGoogle() error = %v", err)
	}
	if info.Google != nil || !info.HasPassword {
		t.Errorf("UnlinkGoogle() = %+v, want no Google and a password", info)
	}
	if n := h.count(t, "user_identities", userID); n != 0 {
		t.Errorf("%d Google accounts linked, want 0", n)
	}
	if h.mail.unlinked[address] != address {
		t.Errorf("the address was told %q was unlinked, want %q", h.mail.unlinked[address], address)
	}
	if _, err := h.svc.UnlinkGoogle(h.ctx, userID); !errors.Is(err, ErrGoogleNotLinked) {
		t.Errorf("unlink again: err = %v, want ErrGoogleNotLinked", err)
	}
	if _, err := h.signIn(address, "Timebox2026x"); err != nil {
		t.Errorf("password after unlinking: err = %v", err)
	}
	if !slices.Contains(h.audit.actions(userID), audit.ActionGoogleUnlinked) {
		t.Errorf("audit log %v lacks %s", h.audit.actions(userID), audit.ActionGoogleUnlinked)
	}
}

// countOf counts want in list.
func countOf(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}
	return n
}
