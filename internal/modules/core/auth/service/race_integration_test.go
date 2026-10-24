package service

// Integration tests for requests racing a change that ends every session,
// for what such a change must take along, and for codes and grants
// redeemed in parallel. A race is staged by holding one request inside its
// transaction (see trackingSubjects) while the other runs. See
// account_integration_test.go for how to run them.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"timebox-be/internal/config"
	"timebox-be/internal/database"
	"timebox-be/internal/modules/core/auth/domain"
	"timebox-be/internal/modules/core/auth/dto"
	"timebox-be/pkg/jwt"
)

// waitForLockWait returns once a session of the test database waits for a
// lock, or done is closed: the request run against a held one either
// waits for it or does not need to.
func (h *harness) waitForLockWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return
		default:
		}
		var waiting int
		err := h.pool.QueryRow(h.ctx, `
			SELECT COUNT(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()
		`).Scan(&waiting)
		if err != nil {
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the racing request neither waited for a lock nor finished")
}

// assertSessionEnded checks that the session behind a token pair is over:
// the refresh token is revoked, the access token refused, and the session
// no longer listed.
func (h *harness) assertSessionEnded(t *testing.T, userID, accessToken, refreshToken string) {
	t.Helper()
	if _, err := h.svc.RefreshToken(h.ctx, refreshToken); !errors.Is(err, ErrRefreshTokenRevoked) {
		t.Errorf("refresh token: err = %v, want ErrRefreshTokenRevoked", err)
	}
	claims, err := jwt.ParseToken(accessToken)
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	subject := h.subject(t, userID)
	if subject.AcceptsTokenIssuedAt(claims.IssuedAt.Time) && subject.AcceptsSession(claims.SessionID) {
		t.Error("access token is still accepted")
	}
	sessions, _ := h.svc.ListSessions(h.ctx, userID, "")
	for _, s := range sessions {
		if s.ID == claims.SessionID {
			t.Errorf("session %s is still live", s.ID)
		}
	}
}

// A refresh that has claimed the old token and stored the new one, but not
// committed, must not leave the new one alive when the session is ended
// meanwhile.
func TestIntegration_RefreshRacingEndOfSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(h *harness, userID, sessionID string) error
	}{
		{"sign out everywhere", func(h *harness, userID, _ string) error { return h.svc.LogoutAll(h.ctx, userID) }},
		{"end the session", func(h *harness, userID, sessionID string) error {
			return h.svc.EndSession(h.ctx, userID, sessionID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			address := randomEmail()
			userID := h.signUp(t, address, "Timebox2026x")
			session, err := h.signIn(address, "Timebox2026x")
			if err != nil {
				t.Fatalf("SignIn() error = %v", err)
			}

			sessionID := sessionOf(t, session.AccessToken)

			held, release := h.subjects.holdNextSubject()
			rotated := make(chan *dto.RefreshTokenResponse, 1)
			go func() {
				resp, err := h.svc.RefreshToken(h.ctx, session.RefreshToken)
				if err != nil {
					t.Errorf("RefreshToken() error = %v", err)
				}
				rotated <- resp
			}()
			<-held

			ended, endErr := make(chan struct{}), error(nil)
			go func() {
				defer close(ended)
				endErr = tc.end(h, userID, sessionID)
			}()
			h.waitForLockWait(t, ended)
			release()
			resp := <-rotated
			<-ended
			if endErr != nil {
				t.Fatalf("ending the session: %v", endErr)
			}
			if resp == nil {
				t.FailNow()
			}

			h.assertSessionEnded(t, userID, resp.AccessToken, resp.RefreshToken)
			if sessions, _ := h.svc.ListSessions(h.ctx, userID, ""); len(sessions) != 0 {
				t.Errorf("%d live sessions, want 0", len(sessions))
			}
		})
	}
}

// A sign-in that has checked the old password and is issuing its session
// when the password is reset: the reset ends that session too, and only
// the session the reset itself signed in lives on.
func TestIntegration_SignInRacingPasswordReset(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	grant := h.resetGrant(t, address)

	held, release := h.subjects.holdNextSubject()
	signedIn := make(chan *dto.SignInResponse, 1)
	go func() {
		resp, err := h.signIn(address, "Timebox2026x")
		if err != nil {
			t.Errorf("SignIn() error = %v", err)
		}
		signedIn <- resp
	}()
	<-held

	var (
		reset    = make(chan struct{})
		owner    *dto.SignInResponse
		resetErr error
	)
	go func() {
		defer close(reset)
		owner, resetErr = h.resetPassword(grant, "Reclaimed2026x")
	}()
	h.waitForLockWait(t, reset)
	release()
	resp := <-signedIn
	<-reset
	if resetErr != nil {
		t.Fatalf("ResetPassword() error = %v", resetErr)
	}
	if resp == nil {
		t.FailNow()
	}

	h.assertSessionEnded(t, userID, resp.AccessToken, resp.RefreshToken)
	h.assertSessionLive(t, userID, owner.AccessToken, owner.RefreshToken)
}

// Checks made against a row read earlier lose once the row has changed:
// a right password whose hash was replaced meanwhile, a reset grant for an
// address the account has left.
func TestIntegration_StaleChecksLose(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	before, err := h.users.FindByID(h.ctx, userID)
	if err != nil {
		t.Fatalf("FindByID() error = %v", err)
	}
	attempt, err := h.svc.reservePasswordAttempt(h.ctx, userID)
	if err != nil || attempt == 0 {
		t.Fatalf("reservePasswordAttempt() = %d, %v", attempt, err)
	}

	h.reset(t, address, "Reclaimed2026x")

	if cleared, err := h.users.ClearPasswordAttempts(h.ctx, userID, before.PasswordHash, attempt); err != nil || cleared {
		t.Errorf("sign-in with the replaced password: cleared = %v, err = %v; want refused", cleared, err)
	}
	if changed, err := h.users.ChangePassword(h.ctx, userID, before.PasswordHash, before.PasswordHash, attempt, time.Now()); err != nil || changed {
		t.Errorf("password change from the replaced password: changed = %v, err = %v; want refused", changed, err)
	}
	if err := h.svc.reauthenticate(h.ctx, before, password("Timebox2026x")); !errors.Is(err, ErrPasswordLocked) {
		t.Errorf("reauthentication with the replaced password: err = %v, want refused", err)
	}

	err = database.WithTx(h.ctx, h.pool, func(ctx context.Context) error {
		return h.svc.confirmReauthTx(ctx, before)
	})
	if !errors.Is(err, ErrReauthRequired) {
		t.Errorf("a change confirmed before the reset: err = %v, want ErrReauthRequired", err)
	}

	grant := h.resetGrant(t, address)
	if _, err := h.pool.Exec(h.ctx, `UPDATE core.users SET email = $2 WHERE id = $1`, userID, randomEmail()); err != nil {
		t.Fatalf("move the account: %v", err)
	}
	if err := h.users.ResetPassword(h.ctx, userID, address, before.PasswordHash, time.Now()); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("reset through the address the account left: err = %v, want ErrNotFound", err)
	}
	if _, err := h.resetPassword(grant, "Moved2026xyz"); !errors.Is(err, ErrInvalidResetToken) {
		t.Errorf("grant for the address the account left: err = %v, want ErrInvalidResetToken", err)
	}
}

// If the new access token cannot be issued, the rotation is undone and the
// presented refresh token still works.
func TestIntegration_RefreshFailureKeepsToken(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	h.signUp(t, address, "Timebox2026x")
	session, err := h.signIn(address, "Timebox2026x")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}

	h.subjects.failNextSubject()
	if _, err := h.svc.RefreshToken(h.ctx, session.RefreshToken); err == nil {
		t.Fatal("RefreshToken() succeeded without a subject")
	}
	if _, err := h.svc.RefreshToken(h.ctx, session.RefreshToken); err != nil {
		t.Errorf("retry with the same token: err = %v", err)
	}
}

// An email change asked for before an account changes hands, or before its
// password changes, must not hand the account back to whoever asked.
func TestIntegration_PendingCodesEndWithPriorAccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		// takeOver ends prior access to the account registered with address.
		takeOver func(t *testing.T, h *harness, userID, address string)
	}{
		{"claim through a password reset", func(t *testing.T, h *harness, _, address string) {
			h.reset(t, address, "Reclaimed2026x")
		}},
		{"claim through Google", func(t *testing.T, h *harness, _, address string) {
			h.google.identity = googleAccount(address)
			if _, err := h.signInWithGoogle(t); err != nil {
				t.Fatalf("SignInWithGoogle() error = %v", err)
			}
		}},
		{"password change", func(t *testing.T, h *harness, userID, _ string) {
			if err := h.svc.ChangePassword(h.ctx, userID, "Timebox2026x", "Changed2026x"); err != nil {
				t.Fatalf("ChangePassword() error = %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			address := randomEmail()
			userID := h.signUp(t, address, "Timebox2026x")
			theirs := randomEmail()
			if _, err := h.svc.RequestEmailChange(h.ctx, userID, theirs, password("Timebox2026x")); err != nil {
				t.Fatalf("RequestEmailChange() error = %v", err)
			}

			tc.takeOver(t, h, userID, address)

			if _, err := h.svc.ConfirmEmailChange(h.ctx, userID, h.mail.change[theirs]); !errors.Is(err, ErrCodeExpired) {
				t.Errorf("email change code from before: err = %v, want ErrCodeExpired", err)
			}
			if _, err := h.svc.ResendEmailChange(h.ctx, userID); !errors.Is(err, ErrEmailChangeNotPending) {
				t.Errorf("resend of the email change from before: err = %v, want ErrEmailChangeNotPending", err)
			}
			if user, _ := h.users.FindByID(h.ctx, userID); user == nil || user.Email != address {
				t.Errorf("account moved away from %s", address)
			}
		})
	}
}

// With EMAIL_VERIFICATION_REQUIRED, a Google sign-in through a linked
// identity is refused once the account's verification was withdrawn, as a
// password sign-in is.
func TestIntegration_GoogleSignInNeedsVerifiedEmail(t *testing.T) {
	h := newHarnessWith(t, config.SecurityConfig{EmailVerificationRequired: true})
	address := randomEmail()
	h.google.identity = googleAccount(address)
	resp, err := h.signInWithGoogle(t)
	if err != nil {
		t.Fatalf("first SignInWithGoogle() error = %v", err)
	}
	userID := resp.User.ID

	if _, err := h.pool.Exec(h.ctx, `UPDATE core.users SET is_email_verified = FALSE WHERE id = $1`, userID); err != nil {
		t.Fatalf("withdraw verification: %v", err)
	}
	_, err = h.signInWithGoogle(t)
	var unverified *EmailNotVerifiedError
	if !errors.As(err, &unverified) || !errors.Is(err, ErrEmailNotVerified) || unverified.Email != address {
		t.Errorf("Google sign-in after verification was withdrawn: err = %v, want EmailNotVerifiedError for %s", err, address)
	}
}

// Wrong codes sent in parallel are checked one at a time: no more than
// OTP_MAX_ATTEMPTS of them are counted, and the code is dead after.
func TestIntegration_CodeGuessBurst(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	code := h.mail.verify[address]

	invalid := 0
	for i, err := range burst(20, func() error {
		_, err := h.verifyEmail(address, wrongCode(code))
		return err
	}) {
		switch {
		case errors.Is(err, ErrInvalidCode):
			invalid++
		case errors.Is(err, ErrTooManyAttempts):
		default:
			t.Fatalf("guess %d: err = %v", i+1, err)
		}
	}
	if invalid != defaultOTPMaxAttempts-1 {
		t.Errorf("%d guesses answered invalid_code, want %d", invalid, defaultOTPMaxAttempts-1)
	}
	if attempts := h.codeAttempts(t, userID, domain.PurposeVerifyEmail); attempts != defaultOTPMaxAttempts {
		t.Errorf("attempts = %d, want %d", attempts, defaultOTPMaxAttempts)
	}
	if _, err := h.verifyEmail(address, code); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("right code after the burst: err = %v, want ErrTooManyAttempts", err)
	}
}

// Of requests racing with the right code, one gets the reset grant; of
// requests racing with that grant, one sets the password.
func TestIntegration_CodeRedeemedOnce(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	if _, err := h.svc.ForgotPassword(h.ctx, address); err != nil {
		t.Fatalf("ForgotPassword() error = %v", err)
	}
	code := h.mail.reset[address]

	var (
		mu    sync.Mutex
		grant string
	)
	won := 0
	for i, err := range burst(10, func() error {
		resp, err := h.svc.VerifyResetCode(h.ctx, address, code)
		if err == nil {
			mu.Lock()
			grant = resp.ResetToken
			mu.Unlock()
		}
		return err
	}) {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, ErrCodeExpired):
			t.Fatalf("redeem %d: err = %v", i+1, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d requests got a grant, want 1", won)
	}

	won = 0
	for i, err := range burst(8, func() error {
		_, err := h.resetPassword(grant, "Reclaimed2026x")
		return err
	}) {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, ErrInvalidResetToken):
			t.Fatalf("reset %d: err = %v", i+1, err)
		}
	}
	if won != 1 {
		t.Errorf("%d resets went through, want 1", won)
	}
	if sessions, _ := h.svc.ListSessions(h.ctx, userID, ""); len(sessions) != 1 {
		t.Errorf("%d live sessions, want the winner's", len(sessions))
	}
}
