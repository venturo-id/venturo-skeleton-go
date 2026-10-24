package service

// Integration tests for the password lock shared by sign-in and the
// signed-in password change; see account_integration_test.go for how to
// run them.

import (
	"errors"
	"sync"
	"testing"
	"time"

	roleRepo "timebox-be/internal/modules/core/role/repository"
	"timebox-be/pkg/jwt"
)

func TestIntegration_ChangePassword(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	session, err := h.signIn(address, "Timebox2026x")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	if _, err := h.svc.ForgotPassword(h.ctx, address); err != nil {
		t.Fatalf("ForgotPassword() error = %v", err)
	}
	pendingReset := h.mail.reset[address]

	if err := h.svc.ChangePassword(h.ctx, userID, "Wrongpass2026", "Newpass2026x"); !errors.Is(err, ErrIncorrectPassword) {
		t.Fatalf("wrong current password: err = %v, want ErrIncorrectPassword", err)
	}
	if err := h.svc.ChangePassword(h.ctx, userID, "Timebox2026x", "Newpass2026x"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}

	if _, err := h.signIn(address, "Timebox2026x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("old password: err = %v, want ErrInvalidCredentials", err)
	}
	if _, err := h.signIn(address, "Newpass2026x"); err != nil {
		t.Errorf("new password: err = %v", err)
	}
	if h.mail.changed[address] != 1 {
		t.Errorf("password-changed emails = %d, want 1", h.mail.changed[address])
	}

	// Every earlier session ends, the one that made the change included.
	if _, err := h.svc.RefreshToken(h.ctx, session.RefreshToken); !errors.Is(err, ErrRefreshTokenRevoked) {
		t.Errorf("refresh token from before the change: err = %v, want ErrRefreshTokenRevoked", err)
	}
	claims, err := jwt.ParseToken(session.AccessToken)
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	subject, err := dbSubjects{roleRepo.NewRoleRepository(h.pool)}.GetSubject(h.ctx, userID)
	if err != nil {
		t.Fatalf("GetSubject() error = %v", err)
	}
	if subject.AcceptsTokenIssuedAt(claims.IssuedAt.Time) {
		t.Error("access token from before the change is still accepted")
	}

	// The right password cleared the wrong guess from the count.
	if count, _ := h.lockState(t, userID); count != 0 {
		t.Errorf("failed_login_count = %d, want 0", count)
	}

	// Codes sent before the change are retired with the sessions.
	if _, err := h.svc.VerifyResetCode(h.ctx, address, pendingReset); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("reset code from before the change: err = %v, want ErrCodeExpired", err)
	}
}

// Wrong guesses through a password change lock the account like wrong
// sign-ins: a stolen access token does not give unlimited guesses.
func TestIntegration_ChangePasswordGuessesLock(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")

	for i := 0; i < defaultMaxLoginAttempts; i++ {
		if err := h.svc.ChangePassword(h.ctx, userID, "Wrongpass2026", "Newpass2026x"); !errors.Is(err, ErrIncorrectPassword) {
			t.Fatalf("guess %d: err = %v, want ErrIncorrectPassword", i+1, err)
		}
	}
	if h.mail.locked[address] != 1 {
		t.Errorf("account-locked emails = %d, want 1", h.mail.locked[address])
	}
	// While locked, a right and a wrong password get the same answer.
	if err := h.svc.ChangePassword(h.ctx, userID, "Timebox2026x", "Newpass2026x"); !errors.Is(err, ErrPasswordLocked) {
		t.Errorf("right password while locked: err = %v, want ErrPasswordLocked", err)
	}
	if err := h.svc.ChangePassword(h.ctx, userID, "Wrongpass2026", "Newpass2026x"); !errors.Is(err, ErrPasswordLocked) {
		t.Errorf("wrong password while locked: err = %v, want ErrPasswordLocked", err)
	}
	if _, err := h.signIn(address, "Timebox2026x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("sign-in while locked: err = %v, want ErrInvalidCredentials", err)
	}
}

// Guesses sent in parallel all read the account before the lock lands;
// still no more than MAX_LOGIN_ATTEMPTS of them are checked.
func TestIntegration_ChangePasswordBurst(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")

	errs := burst(20, func() error {
		return h.svc.ChangePassword(h.ctx, userID, "Wrongpass2026", "Newpass2026x")
	})

	checked := 0
	for i, err := range errs {
		switch {
		case errors.Is(err, ErrIncorrectPassword):
			checked++
		case errors.Is(err, ErrPasswordLocked):
		default:
			t.Fatalf("guess %d: err = %v", i+1, err)
		}
	}
	if checked > defaultMaxLoginAttempts {
		t.Errorf("%d of %d parallel guesses were checked, want at most %d", checked, len(errs), defaultMaxLoginAttempts)
	}
	h.assertLocked(t, userID, address)
}

func TestIntegration_ChangePasswordWithoutPassword(t *testing.T) {
	h := newHarness(t)
	h.google.identity = googleAccount(randomEmail())
	resp, err := h.signInWithGoogle(t)
	if err != nil {
		t.Fatalf("SignInWithGoogle() error = %v", err)
	}

	if err := h.svc.ChangePassword(h.ctx, resp.User.ID, "", "Newpass2026x"); !errors.Is(err, ErrNoPassword) {
		t.Errorf("Google-only account: err = %v, want ErrNoPassword", err)
	}
}

func TestIntegration_SignInBurst(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")

	for i, err := range burst(20, func() error {
		_, err := h.signIn(address, "Wrongpass2026")
		return err
	}) {
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("guess %d: err = %v, want ErrInvalidCredentials", i+1, err)
		}
	}
	h.assertLocked(t, userID, address)
	if _, err := h.signIn(address, "Timebox2026x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("right password while locked: err = %v, want ErrInvalidCredentials", err)
	}
}

// The attempt that reaches the limit locks the account before its
// password is checked; a right password there lifts that lock again.
func TestIntegration_RightPasswordOnLastAttempt(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")

	for i := 0; i < defaultMaxLoginAttempts-1; i++ {
		_, _ = h.signIn(address, "Wrongpass2026")
	}
	if _, err := h.signIn(address, "Timebox2026x"); err != nil {
		t.Fatalf("right password on the last attempt: err = %v", err)
	}
	if count, lockedUntil := h.lockState(t, userID); count != 0 || lockedUntil != nil {
		t.Errorf("after signing in: failed_login_count = %d, locked_until = %v, want 0 and none", count, lockedUntil)
	}
	if h.mail.locked[address] != 0 {
		t.Errorf("account-locked emails = %d, want 0", h.mail.locked[address])
	}
}

// A right password ends the run of wrong ones even when the sign-in is
// refused for another reason, or the owner of an inactive account would
// lock themselves out by trying.
func TestIntegration_RightPasswordNeverLocks(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	if _, err := h.pool.Exec(h.ctx, `UPDATE core.users SET is_active = FALSE WHERE id = $1`, userID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	for i := 0; i < defaultMaxLoginAttempts+1; i++ {
		if _, err := h.signIn(address, "Timebox2026x"); !errors.Is(err, ErrUserNotActive) {
			t.Fatalf("sign-in %d: err = %v, want ErrUserNotActive", i+1, err)
		}
	}
	if count, lockedUntil := h.lockState(t, userID); count != 0 || lockedUntil != nil {
		t.Errorf("failed_login_count = %d, locked_until = %v, want 0 and none", count, lockedUntil)
	}
}

// A lock that has expired starts a new run of attempts; a count left at
// the limit without a lock (a lower MAX_LOGIN_ATTEMPTS, an older release)
// does not lock the owner out for good.
func TestIntegration_LockRecovers(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")

	if _, err := h.pool.Exec(h.ctx,
		`UPDATE core.users SET failed_login_count = $2, locked_until = $3 WHERE id = $1`,
		userID, defaultMaxLoginAttempts, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("expire a lock: %v", err)
	}
	if _, err := h.signIn(address, "Wrongpass2026"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: err = %v", err)
	}
	if count, lockedUntil := h.lockState(t, userID); count != 1 || lockedUntil != nil {
		t.Errorf("after an expired lock: failed_login_count = %d, locked_until = %v, want 1 and none", count, lockedUntil)
	}

	if _, err := h.pool.Exec(h.ctx,
		`UPDATE core.users SET failed_login_count = $2, locked_until = NULL WHERE id = $1`,
		userID, defaultMaxLoginAttempts+4); err != nil {
		t.Fatalf("leave a count at the limit: %v", err)
	}
	if _, err := h.signIn(address, "Timebox2026x"); err != nil {
		t.Errorf("right password with a count past the limit: err = %v", err)
	}
}

// burst runs n calls of fn at once and returns their errors.
func burst(n int, fn func() error) []error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { errs[i] = fn() })
	}
	wg.Wait()
	return errs
}

func (h *harness) lockState(t *testing.T, userID string) (count int, lockedUntil *time.Time) {
	t.Helper()
	err := h.pool.QueryRow(h.ctx, `SELECT failed_login_count, locked_until FROM core.users WHERE id = $1`, userID).
		Scan(&count, &lockedUntil)
	if err != nil {
		t.Fatalf("read lock state: %v", err)
	}
	return count, lockedUntil
}

// assertLocked checks that exactly MAX_LOGIN_ATTEMPTS guesses were counted
// — no more were checked — and that the account is locked, with one email
// to its owner.
func (h *harness) assertLocked(t *testing.T, userID, address string) {
	t.Helper()
	count, lockedUntil := h.lockState(t, userID)
	if count != defaultMaxLoginAttempts {
		t.Errorf("failed_login_count = %d, want %d", count, defaultMaxLoginAttempts)
	}
	if lockedUntil == nil || !lockedUntil.After(time.Now()) {
		t.Errorf("locked_until = %v, want a time in the future", lockedUntil)
	}
	if h.mail.locked[address] != 1 {
		t.Errorf("account-locked emails = %d, want 1", h.mail.locked[address])
	}
}
