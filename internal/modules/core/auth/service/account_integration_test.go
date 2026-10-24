package service

// Integration tests for the emailed-code flows and account claiming. They
// need a migrated PostgreSQL database and run only when TEST_DATABASE_URL
// points at one, e.g.
//
//	TEST_DATABASE_URL='postgres://postgres:secret@localhost:5432/timebox_test?sslmode=disable' \
//	    go test ./internal/modules/core/auth/service/ -run Integration
//
// Every test works on users with fresh random emails and deletes them.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"timebox-be/internal/config"
	apikeyDomain "timebox-be/internal/modules/core/apikey/domain"
	apikeyDTO "timebox-be/internal/modules/core/apikey/dto"
	apikeyRepo "timebox-be/internal/modules/core/apikey/repository"
	"timebox-be/internal/modules/core/auth/domain"
	"timebox-be/internal/modules/core/auth/dto"
	"timebox-be/internal/modules/core/auth/repository"
	roleRepo "timebox-be/internal/modules/core/role/repository"
	userRepo "timebox-be/internal/modules/core/user/repository"
	"timebox-be/internal/shared/audit"
	"timebox-be/internal/shared/authz"
	"timebox-be/pkg/email"
	"timebox-be/pkg/googleauth"
	"timebox-be/pkg/jwt"
)

// capturedMail records the codes the service emails, and counts the
// notices.
type capturedMail struct {
	email.NoOpEmailService
	mu       sync.Mutex
	verify   map[string]string // address → last verification code
	reset    map[string]string // address → last password reset code
	change   map[string]string // new address → last email change code
	welcome  map[string]int    // address → welcome emails
	locked   map[string]int    // address → account-locked emails
	changed  map[string]int    // address → password-changed emails
	moved    map[string]string // old address → the address it moved to
	closed   map[string]int    // address → account-closed emails
	linked   map[string]string // address → Google account last linked to it
	unlinked map[string]string // address → Google account last unlinked from it
}

func (m *capturedMail) SendVerificationCode(to, _, code string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.verify[to] = code
	return nil
}

func (m *capturedMail) SendPasswordResetCode(to, _, code string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reset[to] = code
	return nil
}

func (m *capturedMail) SendEmailChangeCode(to, _, code string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.change[to] = code
	return nil
}

func (m *capturedMail) SendWelcomeEmail(to, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.welcome[to]++
	return nil
}

func (m *capturedMail) SendAccountLockedEmail(to, _ string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locked[to]++
	return nil
}

func (m *capturedMail) SendPasswordChangedEmail(to, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.changed[to]++
	return nil
}

func (m *capturedMail) SendEmailChangedEmail(to, _, newEmail string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.moved[to] = newEmail
	return nil
}

func (m *capturedMail) SendAccountClosedEmail(to, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed[to]++
	return nil
}

func (m *capturedMail) SendGoogleLinkedEmail(to, _, googleEmail string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.linked[to] = googleEmail
	return nil
}

func (m *capturedMail) SendGoogleUnlinkedEmail(to, _, googleEmail string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unlinked[to] = googleEmail
	return nil
}

// dbSubjects serves authz subjects straight from the database.
type dbSubjects struct{ roles *roleRepo.RoleRepository }

func (d dbSubjects) GetSubject(ctx context.Context, userID string) (authz.Subject, error) {
	return d.roles.GetUserSubject(ctx, userID)
}
func (dbSubjects) InvalidateUser(context.Context, string) error { return nil }

// trackingSubjects serves subjects from the database and counts the cache
// invalidations the service asks for. A test can hold or fail the next
// lookup — the one issuing an access token, inside the transaction that
// issues it.
type trackingSubjects struct {
	dbSubjects
	mu          sync.Mutex
	invalidated map[string]int
	next        func() error
}

func (s *trackingSubjects) GetSubject(ctx context.Context, userID string) (authz.Subject, error) {
	s.mu.Lock()
	next := s.next
	s.next = nil
	s.mu.Unlock()
	if next != nil {
		if err := next(); err != nil {
			return authz.Subject{}, err
		}
	}
	return s.dbSubjects.GetSubject(ctx, userID)
}

// holdNextSubject makes the next lookup wait until release is called;
// held is closed once it waits.
func (s *trackingSubjects) holdNextSubject() (held <-chan struct{}, release func()) {
	waiting, released := make(chan struct{}), make(chan struct{})
	s.mu.Lock()
	s.next = func() error {
		close(waiting)
		<-released
		return nil
	}
	s.mu.Unlock()
	return waiting, func() { close(released) }
}

// failNextSubject makes the next lookup fail.
func (s *trackingSubjects) failNextSubject() {
	s.mu.Lock()
	s.next = func() error { return errors.New("subject store unavailable") }
	s.mu.Unlock()
}

func (s *trackingSubjects) InvalidateUser(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidated[userID]++
	return nil
}

func (s *trackingSubjects) invalidations(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.invalidated[userID]
}

// memLimiter stands in for the Redis limiter. Until a test turns them on,
// it runs no resend cooldown and counts no hourly quota, so codes can be
// asked for back to back; once on, both are kept in memory.
type memLimiter struct {
	mu        sync.Mutex
	cooldowns bool
	quotas    bool
	until     map[string]time.Time // cooldown key → when it ends
	sent      map[string]int       // quota key → emails counted
}

// startCooldowns makes codes of one kind to one address a cooldown apart.
func (l *memLimiter) startCooldowns() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cooldowns = true
}

// passCooldowns ends every running cooldown, as if its time had passed.
func (l *memLimiter) passCooldowns() {
	l.mu.Lock()
	defer l.mu.Unlock()
	clear(l.until)
}

// countQuotas holds each account to its hourly email quotas.
func (l *memLimiter) countQuotas() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.quotas = true
}

func (l *memLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.quotas {
		return true, 0, nil
	}
	l.sent[key]++
	if l.sent[key] > limit {
		return false, window, nil
	}
	return true, 0, nil
}

func (l *memLimiter) Hold(_ context.Context, key string, d time.Duration) (bool, time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.cooldowns {
		return true, 0, nil
	}
	if left := time.Until(l.until[key]); left > 0 {
		return false, left, nil
	}
	l.until[key] = time.Now().Add(d)
	return true, 0, nil
}

func (l *memLimiter) Remaining(_ context.Context, key string) (time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return max(time.Until(l.until[key]), 0), nil
}

// keyStore works on API keys through their repository.
type keyStore struct{ repo *apikeyRepo.APIKeyRepository }

func (k keyStore) RevokeAllForUser(ctx context.Context, userID, reason string) error {
	_, err := k.repo.RevokeAllByUser(ctx, userID, reason)
	return err
}

func (k keyStore) DeleteAllForUser(ctx context.Context, userID string) error {
	return k.repo.DeleteAllByUser(ctx, userID)
}

func (k keyStore) ListAllForUser(ctx context.Context, userID string) ([]apikeyDTO.APIKeyResponse, error) {
	keys, err := k.repo.ListAllByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]apikeyDTO.APIKeyResponse, len(keys))
	for i, key := range keys {
		out[i] = apikeyDTO.APIKeyResponse{ID: key.ID, KeyPrefix: key.KeyPrefix, Name: key.Name}
	}
	return out, nil
}

// memAudit keeps the audit log in memory.
type memAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (m *memAudit) Record(ctx context.Context, e audit.Entry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.ActorID == "" {
		e.ActorID = audit.ActorID(ctx)
	}
	m.entries = append(m.entries, e)
}

func (m *memAudit) ListByActor(_ context.Context, userID string, limit int) ([]audit.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []audit.Entry{}
	for i := len(m.entries) - 1; i >= 0 && len(out) < limit; i-- {
		if m.entries[i].ActorID == userID {
			out = append(out, m.entries[i])
		}
	}
	return out, nil
}

// actions lists the actions recorded about userID, in order.
func (m *memAudit) actions(userID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, e := range m.entries {
		if e.TargetID == userID {
			out = append(out, e.Action)
		}
	}
	return out
}

// fakeGoogle stands in for Google: every authorization code exchanges for
// identity, or fails with err.
type fakeGoogle struct {
	identity *googleauth.Identity
	err      error
}

func (f *fakeGoogle) ClientID(platform string) (string, error) {
	return "client-" + platform + ".apps.googleusercontent.com", nil
}

func (f *fakeGoogle) RedirectURIs(string) []string { return nil }

func (f *fakeGoogle) Exchange(context.Context, googleauth.Exchange) (*googleauth.Identity, error) {
	if f.err != nil {
		return nil, f.err
	}
	identity := *f.identity
	return &identity, nil
}

// googleAccount is a Google account whose address Google verified, as
// signed in to just now.
func googleAccount(address string) *googleauth.Identity {
	return &googleauth.Identity{
		Subject: "google-" + uuid.NewString(), Email: address, EmailVerified: true,
		Name: "Google User", IssuedAt: time.Now(),
		Claims: map[string]any{"email": address, "email_verified": true},
	}
}

// googleCode is what the app sends after Google's redirect; fakeGoogle
// takes any.
var googleCode = dto.GoogleCode{
	Code: "4/0Ab-test-code", CodeVerifier: "v3rifier-v3rifier-v3rifier-v3rifier-v3rifier",
	RedirectURI: "http://127.0.0.1:53682/oauth/google", Platform: googleauth.PlatformDesktop,
}

// memAvatars stands in for the photo storage: a Google picture copied
// gets a URL of ours, and deletions are recorded.
type memAvatars struct {
	mu       sync.Mutex
	imported map[string]string // stored URL → the Google picture it copies
	deleted  []string
}

func (a *memAvatars) ImportGooglePicture(_ context.Context, userID, pictureURL string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	url := "https://cdn.timebox.test/avatars/" + userID + "/" + uuid.NewString() + ".jpg"
	a.imported[url] = pictureURL
	return url, nil
}

func (a *memAvatars) Delete(_ context.Context, avatarURL string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.deleted = append(a.deleted, avatarURL)
	return nil
}

type harness struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	svc      *AuthService
	mail     *capturedMail
	users    *userRepo.UserRepository
	apiKeys  *apikeyRepo.APIKeyRepository
	google   *fakeGoogle
	avatars  *memAvatars
	limiter  *memLimiter
	audit    *memAudit
	subjects *trackingSubjects
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, config.SecurityConfig{})
}

// newHarnessWith builds the harness on security settings.
func newHarnessWith(t *testing.T, security config.SecurityConfig) *harness {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	t.Setenv("JWT_SECRET", "integration-test-secret-long-enough-for-hs256")

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	h := &harness{
		ctx:  ctx,
		pool: pool,
		mail: &capturedMail{
			verify: map[string]string{}, reset: map[string]string{}, change: map[string]string{},
			welcome: map[string]int{}, locked: map[string]int{}, changed: map[string]int{},
			moved: map[string]string{}, closed: map[string]int{},
			linked: map[string]string{}, unlinked: map[string]string{},
		},
		users:   userRepo.NewUserRepository(pool),
		apiKeys: apikeyRepo.NewAPIKeyRepository(pool),
		google:  &fakeGoogle{},
		avatars: &memAvatars{imported: map[string]string{}},
		limiter: &memLimiter{until: map[string]time.Time{}, sent: map[string]int{}},
		audit:   &memAudit{},
	}
	roles := roleRepo.NewRoleRepository(pool)
	h.subjects = &trackingSubjects{dbSubjects: dbSubjects{roles}, invalidated: map[string]int{}}
	h.svc = NewAuthService(security, Deps{
		DB:            pool,
		Users:         h.users,
		Identities:    userRepo.NewUserIdentityRepository(pool),
		RefreshTokens: repository.NewRefreshTokenRepository(pool),
		AccountTokens: repository.NewAccountTokenRepository(pool),
		Roles:         roles,
		Subjects:      h.subjects,
		APIKeys:       keyStore{h.apiKeys},
		Limiter:       h.limiter,
		Audit:         h.audit,
		Mailer:        h.mail,
		Google:        h.google,
		Avatars:       h.avatars,
	})
	return h
}

// deleteLater schedules the deletion of a user the test created.
func (h *harness) deleteLater(t *testing.T, userID string) {
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(), `DELETE FROM core.users WHERE id = $1`, userID)
	})
}

// register signs up with req and schedules the account's deletion.
func (h *harness) register(t *testing.T, req *dto.SignUpRequest) *dto.SignUpResponse {
	t.Helper()
	resp, err := h.svc.SignUp(h.ctx, req)
	if err != nil {
		t.Fatalf("SignUp() error = %v", err)
	}
	h.deleteLater(t, resp.User.ID)
	return resp
}

// signUp registers a self-signup account and schedules its deletion.
func (h *harness) signUp(t *testing.T, address, password string) string {
	t.Helper()
	return h.register(t, &dto.SignUpRequest{
		Email: address, Username: "it-" + uuid.NewString()[:12], Password: password,
	}).User.ID
}

func (h *harness) signIn(login, password string) (*dto.SignInResponse, error) {
	return h.svc.SignIn(h.ctx, &dto.SignInRequest{Login: login, Password: password}, domain.DeviceInfo{}, "203.0.113.9")
}

func (h *harness) verifyEmail(address, code string) (*dto.SignInResponse, error) {
	return h.svc.VerifyEmail(h.ctx, address, code, domain.DeviceInfo{}, "203.0.113.9")
}

// verify enters the verification code last sent to address.
func (h *harness) verify(t *testing.T, address string) *dto.SignInResponse {
	t.Helper()
	resp, err := h.verifyEmail(address, h.mail.verify[address])
	if err != nil {
		t.Fatalf("VerifyEmail() error = %v", err)
	}
	return resp
}

// resetGrant asks for a password reset code for address and trades it
// for a reset grant.
func (h *harness) resetGrant(t *testing.T, address string) string {
	t.Helper()
	if _, err := h.svc.ForgotPassword(h.ctx, address); err != nil {
		t.Fatalf("ForgotPassword() error = %v", err)
	}
	grant, err := h.svc.VerifyResetCode(h.ctx, address, h.mail.reset[address])
	if err != nil {
		t.Fatalf("VerifyResetCode() error = %v", err)
	}
	return grant.ResetToken
}

func (h *harness) resetPassword(resetToken, newPassword string) (*dto.SignInResponse, error) {
	return h.svc.ResetPassword(h.ctx, resetToken, newPassword, domain.DeviceInfo{}, "203.0.113.9")
}

// reset sets newPassword on the account of address through an emailed
// code, and returns the session the reset signed in.
func (h *harness) reset(t *testing.T, address, newPassword string) *dto.SignInResponse {
	t.Helper()
	resp, err := h.resetPassword(h.resetGrant(t, address), newPassword)
	if err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}
	return resp
}

// signInWithGoogle signs in as the Google account h.google answers with,
// and schedules the deletion of an account it creates.
func (h *harness) signInWithGoogle(t *testing.T) (*dto.GoogleSignInResponse, error) {
	t.Helper()
	resp, err := h.svc.SignInWithGoogle(h.ctx, &dto.GoogleSignInRequest{GoogleCode: googleCode}, domain.DeviceInfo{}, "203.0.113.9")
	if err == nil && resp.IsNewUser {
		h.deleteLater(t, resp.User.ID)
	}
	return resp, err
}

// createAPIKey gives userID an active API key and returns its ID.
func (h *harness) createAPIKey(t *testing.T, userID string) string {
	t.Helper()
	now := time.Now()
	key := &apikeyDomain.APIKey{
		ID: uuid.NewString(), UserID: userID, KeyID: uuid.NewString()[:8], SecretHash: uuid.NewString() + uuid.NewString()[:28],
		KeyPrefix: "tbx_live_test...", Name: "squatter key", Environment: apikeyDomain.EnvLive,
		ScopedPermissions: []string{"users:read"}, RateLimit: 10, RateLimitWindow: 60,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := h.apiKeys.Create(h.ctx, key); err != nil {
		t.Fatalf("create API key: %v", err)
	}
	return key.ID
}

// codeAttempts returns the wrong guesses counted against userID's latest
// code for purpose.
func (h *harness) codeAttempts(t *testing.T, userID string, purpose domain.AccountTokenPurpose) int {
	t.Helper()
	var attempts int
	err := h.pool.QueryRow(h.ctx, `
		SELECT attempts FROM core.account_tokens
		WHERE user_id = $1 AND purpose = $2
		ORDER BY created_at DESC LIMIT 1`, userID, string(purpose)).Scan(&attempts)
	if err != nil {
		t.Fatalf("read code attempts: %v", err)
	}
	return attempts
}

func randomEmail() string { return "it-" + uuid.NewString()[:12] + "@timebox.test" }

// wrongCode returns a code that is not code.
func wrongCode(code string) string {
	n, _ := strconv.Atoi(code)
	return fmt.Sprintf("%06d", (n+1)%1_000_000)
}

var sixDigits = regexp.MustCompile(`^[0-9]{6}$`)

// codeError returns the refused code err is, failing the test unless it
// is one refused with want.
func codeError(t *testing.T, what string, err, want error) *CodeError {
	t.Helper()
	var codeErr *CodeError
	if !errors.As(err, &codeErr) || !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want a CodeError with %v", what, err, want)
	}
	return codeErr
}

// cooldownError returns the cooldown err is, failing the test unless it
// is one with time left.
func cooldownError(t *testing.T, what string, err error) *CooldownError {
	t.Helper()
	var cooldown *CooldownError
	if !errors.As(err, &cooldown) || !errors.Is(err, ErrResendCooldown) {
		t.Fatalf("%s: err = %v, want a CooldownError", what, err)
	}
	if cooldown.RetryAfter <= 0 || cooldown.RetryAfter > defaultOTPResendCooldown {
		t.Errorf("%s: retry after %v, want within %v", what, cooldown.RetryAfter, defaultOTPResendCooldown)
	}
	return cooldown
}

var (
	codeTTLSeconds  = int(defaultOTPExpiration.Seconds())
	cooldownSeconds = int(defaultOTPResendCooldown.Seconds())
)

func TestIntegration_EmailVerification(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")

	first := h.mail.verify[address]
	if !sixDigits.MatchString(first) {
		t.Fatalf("signup sent verification code %q, want 6 digits", first)
	}
	if h.mail.welcome[address] != 0 {
		t.Errorf("welcome emails before verification = %d, want 0", h.mail.welcome[address])
	}

	// Asking again replaces the first code. Two codes in a row match once
	// in a million: ask until they differ.
	for h.mail.verify[address] == first {
		ticket, err := h.svc.ResendVerificationCode(h.ctx, address)
		if err != nil {
			t.Fatalf("ResendVerificationCode() error = %v", err)
		}
		if ticket.ExpiresIn != codeTTLSeconds {
			t.Errorf("ticket expires in %d s, want %d", ticket.ExpiresIn, codeTTLSeconds)
		}
	}
	// The replaced code is checked against the current one: a wrong guess.
	_, err := h.verifyEmail(address, first)
	if left := codeError(t, "first, replaced code", err, ErrInvalidCode).AttemptsLeft; left != defaultOTPMaxAttempts-1 {
		t.Errorf("first, replaced code: %d attempts left, want %d", left, defaultOTPMaxAttempts-1)
	}

	// The code signs the account in.
	latest := h.mail.verify[address]
	session, err := h.verifyEmail(address, latest)
	if err != nil {
		t.Fatalf("VerifyEmail() error = %v", err)
	}
	if session.User.ID != userID || !session.User.IsEmailVerified {
		t.Errorf("signed in as %s verified=%v, want %s verified", session.User.ID, session.User.IsEmailVerified, userID)
	}
	h.assertSessionLive(t, userID, session.AccessToken, session.RefreshToken)
	if user, _ := h.users.FindByID(h.ctx, userID); user == nil || !user.IsEmailVerified || user.EmailVerifiedAt == nil {
		t.Fatalf("user after verification = %+v, want verified", user)
	}
	if h.mail.welcome[address] != 1 {
		t.Errorf("welcome emails = %d, want 1", h.mail.welcome[address])
	}
	if _, err := h.verifyEmail(address, latest); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("reused code: err = %v, want ErrCodeExpired", err)
	}

	// A verified account gets no more verification codes, and the answer
	// does not tell.
	delete(h.mail.verify, address)
	ticket, err := h.svc.ResendVerificationCode(h.ctx, address)
	if err != nil || h.mail.verify[address] != "" {
		t.Fatalf("resend to a verified account: err = %v, sent = %q", err, h.mail.verify[address])
	}
	if ticket != (dto.CodeTicket{ExpiresIn: codeTTLSeconds}) {
		t.Errorf("ticket = %+v, want the usual one", ticket)
	}

	actions := h.audit.actions(userID)
	for _, want := range []string{audit.ActionEmailVerified, audit.ActionSignIn} {
		if !slices.Contains(actions, want) {
			t.Errorf("audit log %v lacks %s", actions, want)
		}
	}
}

// Wrong codes count against the code; the one that reaches
// OTP_MAX_ATTEMPTS kills it, the right one included.
func TestIntegration_WrongCodes(t *testing.T) {
	h := newHarness(t)
	h.limiter.startCooldowns()
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	code := h.mail.verify[address]

	for i := 1; i < defaultOTPMaxAttempts; i++ {
		_, err := h.verifyEmail(address, wrongCode(code))
		if left := codeError(t, fmt.Sprintf("wrong code %d", i), err, ErrInvalidCode).AttemptsLeft; left != defaultOTPMaxAttempts-i {
			t.Errorf("wrong code %d: %d attempts left, want %d", i, left, defaultOTPMaxAttempts-i)
		}
	}
	if attempts := h.codeAttempts(t, userID, domain.PurposeVerifyEmail); attempts != defaultOTPMaxAttempts-1 {
		t.Errorf("attempts = %d, want %d", attempts, defaultOTPMaxAttempts-1)
	}

	// The last wrong code kills it; a new one may be asked for once the
	// sign-up's cooldown is over.
	_, err := h.verifyEmail(address, wrongCode(code))
	if retry := codeError(t, "last wrong code", err, ErrTooManyAttempts).RetryAfter; retry <= 0 || retry > defaultOTPResendCooldown {
		t.Errorf("last wrong code: retry after %v, want the cooldown's time left", retry)
	}
	if _, err := h.verifyEmail(address, code); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("right code after too many wrong ones: err = %v, want ErrTooManyAttempts", err)
	}
	if user, _ := h.users.FindByID(h.ctx, userID); user == nil || user.IsEmailVerified {
		t.Fatalf("user = %+v, want unverified", user)
	}
	if attempts := h.codeAttempts(t, userID, domain.PurposeVerifyEmail); attempts != defaultOTPMaxAttempts {
		t.Errorf("attempts = %d, want %d: a dead code counts no more", attempts, defaultOTPMaxAttempts)
	}

	_, err = h.svc.ResendVerificationCode(h.ctx, address)
	cooldownError(t, "new code during the cooldown", err)

	h.limiter.passCooldowns()
	if _, err := h.svc.ResendVerificationCode(h.ctx, address); err != nil {
		t.Fatalf("ResendVerificationCode() error = %v", err)
	}
	_, err = h.verifyEmail(address, wrongCode(h.mail.verify[address]))
	if left := codeError(t, "wrong guess at the new code", err, ErrInvalidCode).AttemptsLeft; left != defaultOTPMaxAttempts-1 {
		t.Errorf("new code: %d attempts left, want %d", left, defaultOTPMaxAttempts-1)
	}
	h.verify(t, address)
}

// A code works for OTP_EXPIRATION.
func TestIntegration_CodeExpires(t *testing.T) {
	h := newHarnessWith(t, config.SecurityConfig{OTPExpiration: 2 * time.Minute})
	address := randomEmail()
	resp := h.register(t, &dto.SignUpRequest{Email: address, Username: "it-" + uuid.NewString()[:12], Password: "Timebox2026x"})
	if resp.Verification.ExpiresIn != 120 {
		t.Errorf("verification expires in %d s, want 120", resp.Verification.ExpiresIn)
	}
	var lifetime float64
	err := h.pool.QueryRow(h.ctx, `
		SELECT EXTRACT(EPOCH FROM expires_at - created_at)::float8 FROM core.account_tokens
		WHERE user_id = $1 AND used_at IS NULL`, resp.User.ID).Scan(&lifetime)
	if err != nil || lifetime != 120 {
		t.Errorf("stored code lifetime = %v s (err %v), want 120", lifetime, err)
	}

	if _, err := h.pool.Exec(h.ctx, `
		UPDATE core.account_tokens SET expires_at = NOW() - INTERVAL '1 second'
		WHERE user_id = $1 AND used_at IS NULL`, resp.User.ID); err != nil {
		t.Fatalf("expire the code: %v", err)
	}
	code := h.mail.verify[address]
	if _, err := h.verifyEmail(address, code); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("expired code: err = %v, want ErrCodeExpired", err)
	}
	// Guesses at an expired code are not counted.
	if _, err := h.verifyEmail(address, wrongCode(code)); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("wrong guess at an expired code: err = %v, want ErrCodeExpired", err)
	}
	if attempts := h.codeAttempts(t, resp.User.ID, domain.PurposeVerifyEmail); attempts != 0 {
		t.Errorf("attempts = %d, want 0", attempts)
	}
	// Nor does a code work for another address.
	if _, err := h.verifyEmail(randomEmail(), code); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("code entered for an unknown address: err = %v, want ErrCodeExpired", err)
	}

	if _, err := h.svc.ResendVerificationCode(h.ctx, address); err != nil {
		t.Fatalf("ResendVerificationCode() error = %v", err)
	}
	h.verify(t, address)
}

func TestIntegration_VerificationCodeBoundToAddress(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	code := h.mail.verify[address]

	moved := randomEmail()
	if _, err := h.pool.Exec(h.ctx, `UPDATE core.users SET email = $2 WHERE id = $1`, userID, moved); err != nil {
		t.Fatalf("change email: %v", err)
	}
	if _, err := h.verifyEmail(address, code); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("code sent to the old address, entered for it: err = %v, want ErrCodeExpired", err)
	}
	if _, err := h.verifyEmail(moved, code); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("code sent to the old address, entered for the new one: err = %v, want ErrCodeExpired", err)
	}
	if user, _ := h.users.FindByID(h.ctx, userID); user == nil || user.IsEmailVerified {
		t.Errorf("user = %+v, want unverified", user)
	}
}

// Asking for a code answers the same whether or not an account uses the
// address, and the cooldown runs for every address alike.
func TestIntegration_CodeRequestsAnswerAlike(t *testing.T) {
	h := newHarness(t)
	h.limiter.startCooldowns()
	known := randomEmail()
	h.signUp(t, known, "Timebox2026x")
	h.limiter.passCooldowns()

	for _, tc := range []struct {
		name string
		ask  func(ctx context.Context, address string) (dto.CodeTicket, error)
		sent map[string]string
	}{
		{"verification code", h.svc.ResendVerificationCode, h.mail.verify},
		{"password reset code", h.svc.ForgotPassword, h.mail.reset},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unknown := randomEmail()
			delete(tc.sent, known)

			want := dto.CodeTicket{ExpiresIn: codeTTLSeconds, ResendAvailableIn: cooldownSeconds}
			for _, address := range []string{known, unknown} {
				ticket, err := tc.ask(h.ctx, address)
				if err != nil {
					t.Fatalf("%s: err = %v", address, err)
				}
				if ticket != want {
					t.Errorf("%s: ticket = %+v, want %+v", address, ticket, want)
				}
			}
			if tc.sent[known] == "" {
				t.Error("no code sent to the account")
			}
			if tc.sent[unknown] != "" {
				t.Error("a code was sent to an address no account uses")
			}

			for _, address := range []string{known, unknown} {
				_, err := tc.ask(h.ctx, address)
				cooldownError(t, "asking again for "+address, err)
			}
		})
	}
}

// An inactive account's codes are refused as if the address were
// unknown: the answer never tells that the account exists.
func TestIntegration_CodeChecksHideInactiveAccounts(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	if _, err := h.svc.ForgotPassword(h.ctx, address); err != nil {
		t.Fatalf("ForgotPassword() error = %v", err)
	}
	if _, err := h.pool.Exec(h.ctx, `UPDATE core.users SET is_active = FALSE WHERE id = $1`, userID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	_, err := h.verifyEmail(address, h.mail.verify[address])
	codeError(t, "verification code of an inactive account", err, ErrCodeExpired)
	_, err = h.svc.VerifyResetCode(h.ctx, address, h.mail.reset[address])
	codeError(t, "reset code of an inactive account", err, ErrCodeExpired)

	_, err = h.verifyEmail(randomEmail(), "123456")
	codeError(t, "code for an unknown address", err, ErrCodeExpired)
}

// Past an account's hourly quota, codes are no longer sent, and the
// answer does not change.
func TestIntegration_EmailQuota(t *testing.T) {
	h := newHarness(t)
	h.limiter.countQuotas()
	address := randomEmail()
	h.signUp(t, address, "Timebox2026x") // the first verification code

	for i := 1; i <= defaultMaxVerificationEmails; i++ {
		delete(h.mail.verify, address)
		ticket, err := h.svc.ResendVerificationCode(h.ctx, address)
		if err != nil || ticket.ExpiresIn != codeTTLSeconds {
			t.Fatalf("resend %d: ticket = %+v, err = %v", i, ticket, err)
		}
		if sent := h.mail.verify[address] != ""; sent != (i < defaultMaxVerificationEmails) {
			t.Errorf("verification code %d sent = %v", i+1, sent)
		}
	}
	for i := 1; i <= defaultMaxResetEmails+1; i++ {
		delete(h.mail.reset, address)
		ticket, err := h.svc.ForgotPassword(h.ctx, address)
		if err != nil || ticket.ExpiresIn != codeTTLSeconds {
			t.Fatalf("reset request %d: ticket = %+v, err = %v", i, ticket, err)
		}
		if sent := h.mail.reset[address] != ""; sent != (i <= defaultMaxResetEmails) {
			t.Errorf("reset code %d sent = %v", i, sent)
		}
	}
}

// Without a username, sign-up derives one from the email, as a Google
// sign-up does.
func TestIntegration_SignUpDerivesUsername(t *testing.T) {
	h := newHarness(t)
	local := "it-" + uuid.NewString()[:12]
	address := local + "@timebox.test"

	resp := h.register(t, &dto.SignUpRequest{Email: address, Password: "Timebox2026x"})
	if resp.User.Username != local {
		t.Errorf("username = %q, want %q", resp.User.Username, local)
	}
	if resp.User.Email != address || resp.User.IsEmailVerified || !resp.User.HasPassword || resp.User.Google != nil {
		t.Errorf("user = %+v, want unverified with a password", resp.User)
	}
	want := dto.VerificationSent{Email: address, CodeTicket: dto.CodeTicket{ExpiresIn: codeTTLSeconds}}
	if resp.Verification != want {
		t.Errorf("verification = %+v, want %+v", resp.Verification, want)
	}
	if !sixDigits.MatchString(h.mail.verify[address]) {
		t.Errorf("verification code = %q, want 6 digits", h.mail.verify[address])
	}

	// The same local part at another domain gets a suffix.
	other := h.register(t, &dto.SignUpRequest{Email: local + "@other.test", Password: "Timebox2026x"})
	if u := other.User.Username; len(u) != len(local)+9 || u[:len(local)+1] != local+"-" {
		t.Errorf("second username = %q, want %s-<8 hex>", u, local)
	}

	// A chosen username, or the address, cannot be taken twice.
	if _, err := h.svc.SignUp(h.ctx, &dto.SignUpRequest{Email: randomEmail(), Username: local, Password: "Timebox2026x"}); !errors.Is(err, ErrUsernameAlreadyExists) {
		t.Errorf("taken username: err = %v, want ErrUsernameAlreadyExists", err)
	}
	if _, err := h.svc.SignUp(h.ctx, &dto.SignUpRequest{Email: address, Password: "Timebox2026x"}); !errors.Is(err, ErrEmailAlreadyExists) {
		t.Errorf("taken email: err = %v, want ErrEmailAlreadyExists", err)
	}
}

// With EMAIL_VERIFICATION_REQUIRED, the right password into an unverified
// account is refused with the address the code went to — also when the
// user signed in by username — and the code signs them in.
func TestIntegration_SignInUnverified(t *testing.T) {
	h := newHarnessWith(t, config.SecurityConfig{EmailVerificationRequired: true})
	address := randomEmail()
	resp := h.register(t, &dto.SignUpRequest{Email: address, Username: "it-" + uuid.NewString()[:12], Password: "Timebox2026x"})

	for _, login := range []string{address, resp.User.Username} {
		_, err := h.signIn(login, "Timebox2026x")
		var unverified *EmailNotVerifiedError
		if !errors.As(err, &unverified) || !errors.Is(err, ErrEmailNotVerified) {
			t.Fatalf("sign in as %s: err = %v, want EmailNotVerifiedError", login, err)
		}
		if unverified.Email != address {
			t.Errorf("sign in as %s: email = %q, want %q", login, unverified.Email, address)
		}
	}
	// A wrong password learns nothing about the account.
	if _, err := h.signIn(address, "Wrongpass2026"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password: err = %v, want ErrInvalidCredentials", err)
	}

	session := h.verify(t, address)
	h.assertSessionLive(t, resp.User.ID, session.AccessToken, session.RefreshToken)
	if _, err := h.signIn(address, "Timebox2026x"); err != nil {
		t.Errorf("sign in once verified: err = %v", err)
	}
}

func TestIntegration_PasswordReset(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")
	h.verify(t, address)
	other, err := h.signIn(address, "Timebox2026x")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}

	ticket, err := h.svc.ForgotPassword(h.ctx, address)
	if err != nil {
		t.Fatalf("ForgotPassword() error = %v", err)
	}
	if ticket.ExpiresIn != codeTTLSeconds {
		t.Errorf("ticket expires in %d s, want %d", ticket.ExpiresIn, codeTTLSeconds)
	}
	code := h.mail.reset[address]
	if !sixDigits.MatchString(code) {
		t.Fatalf("reset code = %q, want 6 digits", code)
	}

	// The code is traded for a grant, once.
	_, err = h.svc.VerifyResetCode(h.ctx, address, wrongCode(code))
	if left := codeError(t, "wrong code", err, ErrInvalidCode).AttemptsLeft; left != defaultOTPMaxAttempts-1 {
		t.Errorf("wrong code: %d attempts left, want %d", left, defaultOTPMaxAttempts-1)
	}
	grant, err := h.svc.VerifyResetCode(h.ctx, address, code)
	if err != nil {
		t.Fatalf("VerifyResetCode() error = %v", err)
	}
	if grant.ResetToken == "" || grant.ExpiresIn != int(defaultResetGrantTTL.Seconds()) {
		t.Errorf("grant = %+v, want a token valid for %v", grant, defaultResetGrantTTL)
	}
	if _, err := h.svc.VerifyResetCode(h.ctx, address, code); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("code used twice: err = %v, want ErrCodeExpired", err)
	}
	// Nothing changed yet.
	if _, err := h.signIn(address, "Timebox2026x"); err != nil {
		t.Errorf("password before the reset: err = %v", err)
	}

	// The grant sets the password and signs this client in; every other
	// session ends.
	session, err := h.resetPassword(grant.ResetToken, "Newpass2026x")
	if err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}
	if session.User.ID != userID || !session.User.HasPassword {
		t.Errorf("reset signed in %+v, want %s", session.User, userID)
	}
	h.assertSessionLive(t, userID, session.AccessToken, session.RefreshToken)
	h.assertSessionEnded(t, userID, other.AccessToken, other.RefreshToken)
	if sessions, _ := h.svc.ListSessions(h.ctx, userID, ""); len(sessions) != 1 {
		t.Errorf("%d live sessions, want only the reset's", len(sessions))
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

	for _, tc := range []struct{ name, grant string }{
		{"used grant", grant.ResetToken},
		{"unknown grant", "not-a-reset-token"},
		{"reset code as a grant", code},
	} {
		if _, err := h.resetPassword(tc.grant, "Another2026x"); !errors.Is(err, ErrInvalidResetToken) {
			t.Errorf("%s: err = %v, want ErrInvalidResetToken", tc.name, err)
		}
	}

	expired := h.resetGrant(t, address)
	if _, err := h.pool.Exec(h.ctx, `
		UPDATE core.account_tokens SET expires_at = NOW() - INTERVAL '1 second'
		WHERE user_id = $1 AND purpose = 'reset_grant' AND used_at IS NULL`, userID); err != nil {
		t.Fatalf("expire the grant: %v", err)
	}
	if _, err := h.resetPassword(expired, "Another2026x"); !errors.Is(err, ErrInvalidResetToken) {
		t.Errorf("expired grant: err = %v, want ErrInvalidResetToken", err)
	}

	actions := h.audit.actions(userID)
	if !slices.Contains(actions, audit.ActionPasswordReset) || slices.Contains(actions, audit.ActionAccountClaimed) {
		t.Errorf("audit log %v: want %s and no claim", actions, audit.ActionPasswordReset)
	}
}

func TestIntegration_ResetUnlocksAccount(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Timebox2026x")

	for i := 0; i < defaultMaxLoginAttempts; i++ {
		_, _ = h.signIn(address, "Wrongpass2026")
	}
	if _, err := h.signIn(address, "Timebox2026x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("right password while locked: err = %v, want ErrInvalidCredentials", err)
	}

	h.reset(t, address, "Newpass2026x")
	if count, lockedUntil := h.lockState(t, userID); count != 0 || lockedUntil != nil {
		t.Errorf("after reset: failed_login_count = %d, locked_until = %v, want 0 and none", count, lockedUntil)
	}
	if _, err := h.signIn(address, "Newpass2026x"); err != nil {
		t.Fatalf("after reset: err = %v, want the lock lifted", err)
	}
}

// A squatter registers the victim's address; the victim, owning the
// mailbox, resets the password and takes the account over.
func TestIntegration_ResetClaimsSquattedAccount(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Squatter2026x")
	pendingVerification := h.mail.verify[address]
	keyID := h.createAPIKey(t, userID)
	squatter, err := h.signIn(address, "Squatter2026x")
	if err != nil {
		t.Fatalf("squatter SignIn() error = %v", err)
	}

	owner := h.reset(t, address, "Owner2026xyz")

	h.assertTakenOver(t, userID, keyID, squatter)
	if _, err := h.signIn(address, "Squatter2026x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("squatter's password: err = %v, want ErrInvalidCredentials", err)
	}
	if !slices.Contains(h.audit.actions(userID), audit.ActionAccountClaimed) {
		t.Errorf("audit log %v lacks %s", h.audit.actions(userID), audit.ActionAccountClaimed)
	}
	// The owner's session is not caught by the revocation, and the codes
	// sent before it are retired.
	h.assertSessionLive(t, userID, owner.AccessToken, owner.RefreshToken)
	if _, err := h.verifyEmail(address, pendingVerification); !errors.Is(err, ErrCodeExpired) {
		t.Errorf("verification code from before the reset: err = %v, want ErrCodeExpired", err)
	}
}

// A squatter registers the victim's address; the victim signs in with
// Google and gets the account instead of a dead end.
func TestIntegration_GoogleClaimsSquattedAccount(t *testing.T) {
	h := newHarness(t)
	address := randomEmail()
	userID := h.signUp(t, address, "Squatter2026x")
	keyID := h.createAPIKey(t, userID)
	squatter, err := h.signIn(address, "Squatter2026x")
	if err != nil {
		t.Fatalf("squatter SignIn() error = %v", err)
	}

	h.google.identity = googleAccount(address)
	resp, err := h.signInWithGoogle(t)
	if err != nil {
		t.Fatalf("SignInWithGoogle() error = %v", err)
	}
	if resp.IsNewUser || resp.User.ID != userID {
		t.Fatalf("Google sign-in got user %s (new=%v), want the existing %s", resp.User.ID, resp.IsNewUser, userID)
	}
	if resp.User.HasPassword || !resp.User.IsEmailVerified || resp.User.Google == nil {
		t.Errorf("claimed account = %+v, want no password, verified, Google linked", resp.User)
	}

	h.assertTakenOver(t, userID, keyID, squatter)
	if _, err := h.signIn(address, "Squatter2026x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("squatter's password: err = %v, want ErrInvalidCredentials", err)
	}
	if h.mail.linked[address] != address {
		t.Errorf("the address was told %q was linked, want %q", h.mail.linked[address], address)
	}
	if !slices.Contains(h.audit.actions(userID), audit.ActionAccountClaimed) {
		t.Errorf("audit log %v lacks %s", h.audit.actions(userID), audit.ActionAccountClaimed)
	}

	// The owner's fresh session is not caught by the revocation.
	h.assertSessionLive(t, userID, resp.AccessToken, resp.RefreshToken)
}

// assertTakenOver checks that the account is verified and that the
// squatter's session, access token and API key are dead.
func (h *harness) assertTakenOver(t *testing.T, userID, keyID string, squatter *dto.SignInResponse) {
	t.Helper()

	user, err := h.users.FindByID(h.ctx, userID)
	if err != nil || user == nil || !user.IsEmailVerified {
		t.Fatalf("user = %+v (err %v), want verified", user, err)
	}
	if _, err := h.svc.RefreshToken(h.ctx, squatter.RefreshToken); !errors.Is(err, ErrRefreshTokenRevoked) {
		t.Errorf("squatter's refresh token: err = %v, want ErrRefreshTokenRevoked", err)
	}

	claims, err := jwt.ParseToken(squatter.AccessToken)
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	if h.subject(t, userID).AcceptsTokenIssuedAt(claims.IssuedAt.Time) {
		t.Error("squatter's access token is still accepted")
	}

	key, err := h.apiKeys.FindByID(h.ctx, keyID, userID)
	if err != nil || key == nil || !key.IsRevoked() {
		t.Errorf("squatter's API key = %+v (err %v), want revoked", key, err)
	}
}

// assertSessionLive checks that a token pair just issued works: the
// access token is accepted and the refresh token rotates. The refresh
// token is spent.
func (h *harness) assertSessionLive(t *testing.T, userID, accessToken, refreshToken string) {
	t.Helper()
	claims, err := jwt.ParseToken(accessToken)
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	if claims.UserID != userID {
		t.Errorf("access token of %s, want %s", claims.UserID, userID)
	}
	subject := h.subject(t, userID)
	if !subject.AcceptsTokenIssuedAt(claims.IssuedAt.Time) || !subject.AcceptsSession(claims.SessionID) {
		t.Error("the new access token is refused")
	}
	if _, err := h.svc.RefreshToken(h.ctx, refreshToken); err != nil {
		t.Errorf("the new refresh token: err = %v", err)
	}
}
