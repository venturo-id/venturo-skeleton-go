package service

// Integration tests for what a user manages about their own account:
// sessions, email change, re-authentication, closing the account and the
// data export. See account_integration_test.go for how to run them.

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"timebox-be/internal/config"
	"timebox-be/internal/modules/core/auth/domain"
	"timebox-be/internal/modules/core/auth/dto"
	roleRepo "timebox-be/internal/modules/core/role/repository"
	"timebox-be/internal/shared/audit"
	"timebox-be/internal/shared/authz"
	"timebox-be/pkg/googleauth"
	"timebox-be/pkg/jwt"
)

func (h *harness) subject(t *testing.T, userID string) authz.Subject {
	t.Helper()
	subject, err := dbSubjects{roleRepo.NewRoleRepository(h.pool)}.GetSubject(h.ctx, userID)
	if err != nil {
		t.Fatalf("GetSubject() error = %v", err)
	}
	return subject
}

func sessionOf(t *testing.T, accessToken string) string {
	t.Helper()
	claims, err := jwt.ParseToken(accessToken)
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	if claims.SessionID == "" {
		t.Fatal("access token carries no session id")
	}
	return claims.SessionID
}

func (h *harness) count(t *testing.T, table, userID string) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(h.ctx, `SELECT COUNT(*) FROM core.`+table+` WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// password is the proof of the current password.
func password(p string) Reauth { return Reauth{Password: p} }

// withGoogle is the proof of a Google sign-in into the account h.google
// answers with.
func withGoogle() Reauth {
	exchange := googleCode.Exchange()
	return Reauth{Google: &exchange}
}

func TestIntegration_Sessions(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")

	first, err := h.signIn(address, "Timebox2026x")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	second, err := h.signIn(address, "Timebox2026x")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	firstID, secondID := sessionOf(t, first.AccessToken), sessionOf(t, second.AccessToken)

	// Refreshing keeps the session.
	refreshed, err := h.svc.RefreshToken(h.ctx, first.RefreshToken)
	if err != nil {
		t.Fatalf("RefreshToken() error = %v", err)
	}
	if got := sessionOf(t, refreshed.AccessToken); got != firstID {
		t.Errorf("refreshed token session = %s, want %s", got, firstID)
	}

	sessions, err := h.svc.ListSessions(h.ctx, userID, secondID)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("ListSessions() = %d sessions, want 2", len(sessions))
	}
	for _, s := range sessions {
		if s.Current != (s.ID == secondID) {
			t.Errorf("session %s current = %v", s.ID, s.Current)
		}
	}

	// Ending the first session from the second: its tokens stop working
	// at once, the other session's keep working.
	before := h.subjects.invalidations(userID)
	if err := h.svc.EndSession(h.ctx, userID, firstID); err != nil {
		t.Fatalf("EndSession() error = %v", err)
	}
	if h.subjects.invalidations(userID) == before {
		t.Error("ending a session left the cached subject in place")
	}
	subject := h.subject(t, userID)
	if subject.AcceptsSession(firstID) {
		t.Error("access token of the ended session is still accepted")
	}
	if !subject.AcceptsSession(secondID) {
		t.Error("access token of the other session is refused")
	}
	if _, err := h.svc.RefreshToken(h.ctx, refreshed.RefreshToken); !errors.Is(err, ErrRefreshTokenRevoked) {
		t.Errorf("refresh token of the ended session: err = %v, want ErrRefreshTokenRevoked", err)
	}
	if err := h.svc.EndSession(h.ctx, userID, firstID); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("ending it again: err = %v, want ErrSessionNotFound", err)
	}
	if err := h.svc.EndSession(h.ctx, uuid.NewString(), secondID); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("ending someone else's session: err = %v, want ErrSessionNotFound", err)
	}
	third, err := h.signIn(address, "Timebox2026x")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	if !third.User.HasPassword {
		t.Error("has_password = false for an account with a password")
	}
	thirdID := sessionOf(t, third.AccessToken)
	if _, err := h.pool.Exec(h.ctx, `UPDATE core.refresh_tokens SET expires_at = NOW() - INTERVAL '1 minute' WHERE session_id = $1`, thirdID); err != nil {
		t.Fatalf("expire a session: %v", err)
	}
	if err := h.svc.EndSession(h.ctx, userID, thirdID); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("ending an expired session: err = %v, want ErrSessionNotFound", err)
	}

	// A plain logout ends that session's access token too.
	before = h.subjects.invalidations(userID)
	if err := h.svc.Logout(h.ctx, userID, second.RefreshToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if h.subjects.invalidations(userID) == before {
		t.Error("logout left the cached subject in place")
	}
	if h.subject(t, userID).AcceptsSession(secondID) {
		t.Error("access token is accepted after logout")
	}
	if sessions, _ := h.svc.ListSessions(h.ctx, userID, ""); len(sessions) != 0 {
		t.Errorf("after both ended: %d sessions, want 0", len(sessions))
	}

	actions := h.audit.actions(userID)
	for _, want := range []string{audit.ActionSessionEnded, audit.ActionLogout} {
		if !slices.Contains(actions, want) {
			t.Errorf("audit log %v lacks %s", actions, want)
		}
	}
}

// A session whose email became unverified while EMAIL_VERIFICATION_REQUIRED
// is on cannot be extended.
func TestIntegration_RefreshNeedsVerifiedEmail(t *testing.T) {
	h := newHarnessWith(t, config.SecurityConfig{EmailVerificationRequired: true})
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	session := h.verify(t, address)

	if _, err := h.pool.Exec(h.ctx, `UPDATE core.users SET is_email_verified = FALSE WHERE id = $1`, userID); err != nil {
		t.Fatalf("withdraw verification: %v", err)
	}
	if _, err := h.svc.RefreshToken(h.ctx, session.RefreshToken); !errors.Is(err, ErrEmailNotVerified) {
		t.Fatalf("refresh: err = %v, want ErrEmailNotVerified", err)
	}
	// The session is over, its access token included.
	if h.subject(t, userID).AcceptsSession(sessionOf(t, session.AccessToken)) {
		t.Error("access token of the refused session is still accepted")
	}
	if _, err := h.svc.RefreshToken(h.ctx, session.RefreshToken); !errors.Is(err, ErrRefreshTokenRevoked) {
		t.Errorf("the refused token again: err = %v, want ErrRefreshTokenRevoked", err)
	}
}

func TestIntegration_EmailChange(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	pendingVerification := h.mail.verify[address]
	newAddress := randomEmail()

	other := randomEmail()
	h.signUp(t, other, "Timebox2026x")

	both := withGoogle()
	both.Password = "Timebox2026x"
	for _, tc := range []struct {
		name     string
		newEmail string
		proof    Reauth
		want     error
	}{
		{"no proof", newAddress, Reauth{}, ErrReauthRequired},
		{"two proofs", newAddress, both, ErrReauthRequired},
		{"wrong password", newAddress, password("Wrongpass2026"), ErrIncorrectPassword},
		{"same address", address, password("Timebox2026x"), ErrEmailUnchanged},
		{"same address, other case", "  " + strings.ToUpper(address), password("Timebox2026x"), ErrEmailUnchanged},
		{"address of another account", other, password("Timebox2026x"), ErrEmailAlreadyExists},
	} {
		if _, err := h.svc.RequestEmailChange(h.ctx, userID, tc.newEmail, tc.proof); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if len(h.mail.change) != 0 {
		t.Fatalf("refused requests sent codes: %v", h.mail.change)
	}

	sent, err := h.svc.RequestEmailChange(h.ctx, userID, newAddress, password("Timebox2026x"))
	if err != nil {
		t.Fatalf("RequestEmailChange() error = %v", err)
	}
	if sent.NewEmail != newAddress || sent.ExpiresIn != codeTTLSeconds {
		t.Errorf("RequestEmailChange() = %+v, want a code to %s", sent, newAddress)
	}
	code := h.mail.change[newAddress]
	if !sixDigits.MatchString(code) {
		t.Fatalf("code sent to the new address = %q, want 6 digits", code)
	}
	if user, _ := h.users.FindByID(h.ctx, userID); user.Email != address {
		t.Fatalf("email before confirming = %s, want unchanged %s", user.Email, address)
	}

	_, err = h.svc.ConfirmEmailChange(h.ctx, userID, wrongCode(code))
	if left := codeError(t, "wrong code", err, ErrInvalidCode).AttemptsLeft; left != defaultOTPMaxAttempts-1 {
		t.Errorf("wrong code: %d attempts left, want %d", left, defaultOTPMaxAttempts-1)
	}
	// The code is the account's own: another account cannot redeem it.
	otherID := h.signUp(t, randomEmail(), "Timebox2026x")
	if _, err := h.svc.ConfirmEmailChange(h.ctx, otherID, code); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("code entered by another account: err = %v, want ErrCodeExpired", err)
	}

	info, err := h.svc.ConfirmEmailChange(h.ctx, userID, code)
	if err != nil {
		t.Fatalf("ConfirmEmailChange() error = %v", err)
	}
	if info.Email != newAddress || !info.IsEmailVerified {
		t.Errorf("ConfirmEmailChange() = %+v, want %s verified", info, newAddress)
	}
	user, _ := h.users.FindByID(h.ctx, userID)
	if user.Email != newAddress || !user.IsEmailVerified {
		t.Errorf("after confirming: email = %s verified = %v, want %s verified", user.Email, user.IsEmailVerified, newAddress)
	}
	if h.mail.moved[address] != newAddress {
		t.Errorf("old address was told %q, want %q", h.mail.moved[address], newAddress)
	}
	if _, err := h.svc.ConfirmEmailChange(h.ctx, userID, code); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("reused code: err = %v, want ErrCodeExpired", err)
	}
	// A code sent to the old address no longer works, whichever address
	// it is entered for.
	for _, entered := range []string{address, newAddress} {
		if _, err := h.verifyEmail(entered, pendingVerification); !errors.Is(err, ErrCodeExpired) {
			t.Errorf("verification code of the old address, entered for %s: err = %v, want ErrCodeExpired", entered, err)
		}
	}
	if _, err := h.signIn(newAddress, "Timebox2026x"); err != nil {
		t.Errorf("sign in with the new address: err = %v", err)
	}
	if _, err := h.signIn(address, "Timebox2026x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("sign in with the old address: err = %v, want ErrInvalidCredentials", err)
	}

	actions := h.audit.actions(userID)
	for _, want := range []string{audit.ActionEmailChangeRequested, audit.ActionEmailChanged} {
		if !slices.Contains(actions, want) {
			t.Errorf("audit log %v lacks %s", actions, want)
		}
	}
}

// A pending email change can have its code sent again, without the proof;
// once confirmed, or dead of wrong guesses, nothing is pending.
func TestIntegration_EmailChangeResend(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	if _, err := h.svc.ResendEmailChange(h.ctx, userID); !errors.Is(err, ErrEmailChangeNotPending) {
		t.Errorf("resend with nothing pending: err = %v, want ErrEmailChangeNotPending", err)
	}

	newAddress := randomEmail()
	if _, err := h.svc.RequestEmailChange(h.ctx, userID, newAddress, password("Timebox2026x")); err != nil {
		t.Fatalf("RequestEmailChange() error = %v", err)
	}
	first := h.mail.change[newAddress]
	for h.mail.change[newAddress] == first {
		sent, err := h.svc.ResendEmailChange(h.ctx, userID)
		if err != nil {
			t.Fatalf("ResendEmailChange() error = %v", err)
		}
		if sent.NewEmail != newAddress || sent.ExpiresIn != codeTTLSeconds {
			t.Errorf("ResendEmailChange() = %+v, want a code to %s", sent, newAddress)
		}
	}
	// The replaced code is checked against the current one: a wrong guess.
	if _, err := h.svc.ConfirmEmailChange(h.ctx, userID, first); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("replaced code: err = %v, want ErrInvalidCode", err)
	}

	// Dead of wrong guesses, the change is still pending: a new code goes
	// to the address the caller chose.
	latest := h.mail.change[newAddress]
	for range defaultOTPMaxAttempts - 1 {
		_, _ = h.svc.ConfirmEmailChange(h.ctx, userID, wrongCode(latest))
	}
	if _, err := h.svc.ConfirmEmailChange(h.ctx, userID, latest); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("right code after too many wrong ones: err = %v, want ErrTooManyAttempts", err)
	}
	if _, err := h.svc.ResendEmailChange(h.ctx, userID); err != nil {
		t.Fatalf("resend after the code died: err = %v", err)
	}
	if h.mail.change[newAddress] == latest {
		t.Fatal("no new code after the code died")
	}

	// Confirmed: nothing pending any more.
	if _, err := h.svc.ConfirmEmailChange(h.ctx, userID, h.mail.change[newAddress]); err != nil {
		t.Fatalf("ConfirmEmailChange() error = %v", err)
	}
	if _, err := h.svc.ResendEmailChange(h.ctx, userID); !errors.Is(err, ErrEmailChangeNotPending) {
		t.Errorf("resend after confirming: err = %v, want ErrEmailChangeNotPending", err)
	}
}

// Email change codes are a cooldown apart — checked before the proof, so
// a guess at the password waits too — and an account gets a limited
// number of them an hour.
func TestIntegration_EmailChangeLimits(t *testing.T) {
	h := newHarness(t)
	h.limiter.startCooldowns()
	h.limiter.countQuotas()
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	newAddress := randomEmail()

	sent, err := h.svc.RequestEmailChange(h.ctx, userID, newAddress, password("Timebox2026x"))
	if err != nil {
		t.Fatalf("RequestEmailChange() error = %v", err)
	}
	if sent.ResendAvailableIn != cooldownSeconds {
		t.Errorf("resend available in %d s, want %d", sent.ResendAvailableIn, cooldownSeconds)
	}
	_, err = h.svc.RequestEmailChange(h.ctx, userID, randomEmail(), password("Wrongpass2026"))
	cooldownError(t, "second request", err)
	if count, _ := h.lockState(t, userID); count != 0 {
		t.Errorf("failed_login_count = %d, want 0: the password was checked during the cooldown", count)
	}
	_, err = h.svc.ResendEmailChange(h.ctx, userID)
	cooldownError(t, "resend", err)

	// The request was the first code this hour; resends make up the rest.
	for i := 2; i <= defaultMaxVerificationEmails; i++ {
		h.limiter.passCooldowns()
		if _, err := h.svc.ResendEmailChange(h.ctx, userID); err != nil {
			t.Fatalf("code %d: err = %v", i, err)
		}
	}
	h.limiter.passCooldowns()
	_, err = h.svc.ResendEmailChange(h.ctx, userID)
	var quota *EmailQuotaError
	if !errors.As(err, &quota) || !errors.Is(err, ErrEmailQuotaExceeded) || quota.RetryAfter <= 0 {
		t.Errorf("code past the quota: err = %v, want EmailQuotaError with a retry time", err)
	}
}

// An account without a password proves itself with a recent Google
// sign-in into the Google account linked to it.
func TestIntegration_GoogleReauth(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	google := googleAccount(address)
	h.google.identity = google
	resp, err := h.signInWithGoogle(t)
	if err != nil {
		t.Fatalf("SignInWithGoogle() error = %v", err)
	}
	userID := resp.User.ID
	if resp.User.HasPassword {
		t.Error("has_password = true for a Google-only account")
	}
	newAddress := randomEmail()

	if _, err := h.svc.RequestEmailChange(h.ctx, userID, newAddress, password("Anything2026x")); !errors.Is(err, ErrNoPassword) {
		t.Errorf("password on an account without one: err = %v, want ErrNoPassword", err)
	}

	stale := *google
	stale.IssuedAt = time.Now().Add(-10 * time.Minute)
	stranger := *google
	stranger.Subject = "google-" + uuid.NewString()
	for _, tc := range []struct {
		name     string
		identity *googleauth.Identity
		err      error
		want     error
	}{
		{"Google sign-in 10 minutes old", &stale, nil, ErrGoogleReauthStale},
		{"another Google account", &stranger, nil, ErrGoogleAccountMismatch},
		{"Google refused the code", google, errors.New("invalid_grant"), ErrGoogleAuthFailed},
	} {
		h.google.identity, h.google.err = tc.identity, tc.err
		if _, err := h.svc.RequestEmailChange(h.ctx, userID, newAddress, withGoogle()); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if len(h.mail.change) != 0 {
		t.Fatalf("refused requests sent codes: %v", h.mail.change)
	}

	h.google.identity, h.google.err = google, nil
	if _, err := h.svc.RequestEmailChange(h.ctx, userID, newAddress, withGoogle()); err != nil {
		t.Errorf("fresh Google sign-in: err = %v", err)
	}
	if err := h.svc.CloseAccount(h.ctx, userID, withGoogle()); err != nil {
		t.Errorf("CloseAccount() with a fresh Google sign-in: err = %v", err)
	}
	if h.mail.closed[address] != 1 {
		t.Errorf("account-closed emails = %d, want 1", h.mail.closed[address])
	}
}

func TestIntegration_CloseAccount(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	h.createAPIKey(t, userID)
	if _, err := h.signIn(address, "Timebox2026x"); err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	if _, err := h.pool.Exec(h.ctx, `
		INSERT INTO core.user_roles (user_id, role_id, created_at)
		SELECT $1, id, NOW() FROM core.roles WHERE code = 'administrator'`, userID); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	if _, err := h.pool.Exec(h.ctx, `
		INSERT INTO core.user_identities (id, user_id, provider, provider_user_id, email, raw_profile, created_at, updated_at)
		VALUES ($1, $2, 'google', $3, $4, '{"name": "Someone"}', NOW(), NOW())`,
		uuid.NewString(), userID, "google-"+uuid.NewString(), address); err != nil {
		t.Fatalf("link Google: %v", err)
	}
	photo := "https://cdn.timebox.test/avatars/" + userID + "/photo.jpg"
	if _, err := h.pool.Exec(h.ctx, `UPDATE core.users SET avatar_url = $2 WHERE id = $1`, userID, photo); err != nil {
		t.Fatalf("set avatar: %v", err)
	}

	if err := h.svc.CloseAccount(h.ctx, userID, password("Wrongpass2026")); !errors.Is(err, ErrIncorrectPassword) {
		t.Fatalf("wrong password: err = %v, want ErrIncorrectPassword", err)
	}
	if err := h.svc.CloseAccount(h.ctx, userID, Reauth{}); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("no proof: err = %v, want ErrReauthRequired", err)
	}
	if len(h.avatars.deleted) != 0 {
		t.Fatalf("refused closings deleted %v", h.avatars.deleted)
	}
	if err := h.svc.CloseAccount(h.ctx, userID, password("Timebox2026x")); err != nil {
		t.Fatalf("CloseAccount() error = %v", err)
	}

	var (
		email, username          string
		noPassword, noName       bool
		noAvatar                 bool
		deleted, active, invalid bool
	)
	err := h.pool.QueryRow(h.ctx, `
		SELECT email, username, password_hash IS NULL, full_name IS NULL, avatar_url IS NULL,
		       deleted_at IS NOT NULL, is_active, tokens_valid_after IS NOT NULL
		FROM core.users WHERE id = $1`, userID).
		Scan(&email, &username, &noPassword, &noName, &noAvatar, &deleted, &active, &invalid)
	if err != nil {
		t.Fatalf("read closed account: %v", err)
	}
	if email != userID+"@closed.invalid" || username[:7] != "closed-" || !noPassword || !noName || !noAvatar || !deleted || active || !invalid {
		t.Errorf("closed account = %s %s nopassword=%v noname=%v noavatar=%v deleted=%v active=%v epoch=%v",
			email, username, noPassword, noName, noAvatar, deleted, active, invalid)
	}
	for _, table := range []string{"user_identities", "refresh_tokens", "account_tokens", "api_keys", "user_roles"} {
		if n := h.count(t, table, userID); n != 0 {
			t.Errorf("%s left: %d rows", table, n)
		}
	}
	if !slices.Equal(h.avatars.deleted, []string{photo}) {
		t.Errorf("photos deleted = %v, want [%s]", h.avatars.deleted, photo)
	}
	if h.subject(t, userID).Active {
		t.Error("the closed account's subject is still active")
	}
	if h.mail.closed[address] != 1 {
		t.Errorf("account-closed emails = %d, want 1", h.mail.closed[address])
	}
	if _, err := h.signIn(address, "Timebox2026x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("sign in to the closed account: err = %v, want ErrInvalidCredentials", err)
	}
	// The address is free again.
	h.signUp(t, address, "Timebox2026x")
	if !slices.Contains(h.audit.actions(userID), audit.ActionAccountClosed) {
		t.Errorf("audit log %v lacks %s", h.audit.actions(userID), audit.ActionAccountClosed)
	}
}

func TestIntegration_SuperAdminCannotClose(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	if _, err := h.pool.Exec(h.ctx, `
		INSERT INTO core.user_roles (user_id, role_id, created_at)
		SELECT $1, id, NOW() FROM core.roles WHERE code = 'super_admin'`, userID); err != nil {
		t.Fatalf("assign super_admin: %v", err)
	}

	if err := h.svc.CloseAccount(h.ctx, userID, password("Timebox2026x")); !errors.Is(err, ErrSuperAdminCannotClose) {
		t.Errorf("CloseAccount() err = %v, want ErrSuperAdminCannotClose", err)
	}
	if user, _ := h.users.FindByID(h.ctx, userID); user == nil {
		t.Error("the super admin's account was closed")
	}
}

func TestIntegration_ExportAccount(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	h.createAPIKey(t, userID)
	session, err := h.signIn(address, "Timebox2026x")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}

	export, err := h.svc.ExportAccount(h.ctx, userID, sessionOf(t, session.AccessToken))
	if err != nil {
		t.Fatalf("ExportAccount() error = %v", err)
	}
	if export.Profile.Email != address || export.Profile.Timezone != "Asia/Jakarta" || !export.Profile.HasPassword {
		t.Errorf("profile = %+v", export.Profile)
	}
	if len(export.Sessions) != 1 || !export.Sessions[0].Current {
		t.Errorf("sessions = %+v, want the current one", export.Sessions)
	}
	if len(export.APIKeys) != 1 {
		t.Errorf("api keys = %d, want 1", len(export.APIKeys))
	}
	var actions []string
	for _, e := range export.Activity {
		actions = append(actions, e.Action)
	}
	for _, want := range []string{audit.ActionSignUp, audit.ActionSignIn} {
		if !slices.Contains(actions, want) {
			t.Errorf("activity %v lacks %s", actions, want)
		}
	}
}

func TestIntegration_Timezone(t *testing.T) {
	h := newHarness(t)
	zone := "America/New_York"
	resp := h.register(t, &dto.SignUpRequest{
		Email: randomEmail(), Username: "it-" + uuid.NewString()[:12], Password: "Timebox2026x", Timezone: &zone,
	})
	if resp.User.Timezone != zone {
		t.Errorf("sign-up response timezone = %q, want %q", resp.User.Timezone, zone)
	}
	if user, _ := h.users.FindByID(h.ctx, resp.User.ID); user.Timezone != zone {
		t.Errorf("stored timezone = %q, want %q", user.Timezone, zone)
	}

	// A Google sign-up takes the zone it is sent; a later Google sign-in
	// leaves it alone.
	h.google.identity = googleAccount(randomEmail())
	google, err := h.svc.SignInWithGoogle(h.ctx, &dto.GoogleSignInRequest{GoogleCode: googleCode, Timezone: &zone}, domain.DeviceInfo{}, "203.0.113.9")
	if err != nil {
		t.Fatalf("SignInWithGoogle() error = %v", err)
	}
	h.deleteLater(t, google.User.ID)
	if google.User.Timezone != zone {
		t.Errorf("Google sign-up timezone = %q, want %q", google.User.Timezone, zone)
	}
	other := "Europe/Berlin"
	again, err := h.svc.SignInWithGoogle(h.ctx, &dto.GoogleSignInRequest{GoogleCode: googleCode, Timezone: &other}, domain.DeviceInfo{}, "203.0.113.9")
	if err != nil {
		t.Fatalf("second SignInWithGoogle() error = %v", err)
	}
	if again.User.Timezone != zone {
		t.Errorf("timezone after signing in again = %q, want %q", again.User.Timezone, zone)
	}
}
