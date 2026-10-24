package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"timebox-be/internal/shared/authz"
	"timebox-be/internal/shared/ratelimit"
	jwtpkg "timebox-be/pkg/jwt"
)

const testJWTSecret = "middleware-test-secret-long-enough-for-hs256"

// fakeSubjects serves fixed subjects by user ID.
type fakeSubjects map[string]authz.Subject

func (f fakeSubjects) GetSubject(_ context.Context, userID string) (authz.Subject, error) {
	return f[userID], nil
}

// fakeKeys authenticates every API key as owner with the given scope, or
// fails with err.
type fakeKeys struct {
	owner string
	scope []string
	err   error
}

func (f fakeKeys) ValidateAndGetClaims(context.Context, string, string, string, string) (*jwtpkg.Claims, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &jwtpkg.Claims{UserID: f.owner, ScopedPermissions: f.scope}, nil
}

func setup(t *testing.T, subjects fakeSubjects, keys APIKeyValidator) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", testJWTSecret)

	prevAuthz, prevKeys := authzService, apiKeyValidator
	authzService, apiKeyValidator = subjects, keys
	t.Cleanup(func() { authzService, apiKeyValidator = prevAuthz, prevKeys })
}

func bearer(t *testing.T, userID string, isSuperAdminClaim bool) string {
	t.Helper()
	token, err := jwtpkg.GenerateToken(userID, userID+"@example.com", userID, userID, isSuperAdminClaim, nil, "")
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	return "Bearer " + token
}

// serve runs one request through JWTAuth followed by guards, ending in a
// 200 handler.
func serve(headers map[string]string, guards ...gin.HandlerFunc) *httptest.ResponseRecorder {
	r := gin.New()
	handlers := append([]gin.HandlerFunc{JWTAuth()}, guards...)
	handlers = append(handlers, func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/", handlers...)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestJWTAuth_RejectsInactiveUserWithValidToken(t *testing.T) {
	setup(t, fakeSubjects{"u1": {Active: false}}, nil)

	w := serve(map[string]string{"Authorization": bearer(t, "u1", false)})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a deactivated user", w.Code)
	}
}

func TestRequirePermission_UsesLiveSubjectNotTokenClaim(t *testing.T) {
	// The token still says super admin, but the role has since been removed.
	setup(t, fakeSubjects{"u1": {Active: true, Permissions: []string{"roles:read"}}}, nil)

	w := serve(map[string]string{"Authorization": bearer(t, "u1", true)}, RequirePermission("roles:delete"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: the stale is_super_admin claim must not bypass", w.Code)
	}

	w = serve(map[string]string{"Authorization": bearer(t, "u1", true)}, RequirePermission("roles:read"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a permission the user holds", w.Code)
	}
}

func TestRequirePermission_LiveSuperAdminBypasses(t *testing.T) {
	setup(t, fakeSubjects{"u1": {Active: true, SuperAdmin: true}}, nil)

	w := serve(map[string]string{"Authorization": bearer(t, "u1", false)}, RequirePermission("roles:delete"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a current super admin", w.Code)
	}
}

func TestAPIKey_UsesKeyScopeNotOwnerSuperAdmin(t *testing.T) {
	setup(t,
		fakeSubjects{"owner": {Active: true, SuperAdmin: true}},
		fakeKeys{owner: "owner", scope: []string{"roles:read"}},
	)
	key := map[string]string{"X-API-Key": "tbx_live_abc12345_secret"}

	if w := serve(key, RequirePermission("roles:read")); w.Code != http.StatusOK {
		t.Fatalf("in-scope: status = %d, want 200", w.Code)
	}
	if w := serve(key, RequirePermission("roles:delete")); w.Code != http.StatusForbidden {
		t.Fatalf("out of scope: status = %d, want 403 even though the owner is super admin", w.Code)
	}
}

func TestJWTAuth_RejectsTokenIssuedBeforeSessionsWereRevoked(t *testing.T) {
	setup(t, fakeSubjects{"u1": {Active: true, TokensValidAfter: time.Now().Add(time.Hour).UnixMilli()}}, nil)

	w := serve(map[string]string{"Authorization": bearer(t, "u1", false)})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a token issued before sign-out-everywhere", w.Code)
	}

	setup(t, fakeSubjects{"u1": {Active: true, TokensValidAfter: time.Now().Add(-time.Hour).UnixMilli()}}, nil)
	if w := serve(map[string]string{"Authorization": bearer(t, "u1", false)}); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a token issued after the revocation", w.Code)
	}
}

func TestAPIKey_ScopeNarrowedToWhatOwnerStillHolds(t *testing.T) {
	setup(t,
		fakeSubjects{"owner": {Active: true, Permissions: []string{"roles:read"}}},
		fakeKeys{owner: "owner", scope: []string{"roles:read", "users:read"}},
	)
	key := map[string]string{"X-API-Key": "tbx_live_abc12345_secret"}

	if w := serve(key, RequirePermission("roles:read")); w.Code != http.StatusOK {
		t.Fatalf("held by owner: status = %d, want 200", w.Code)
	}
	if w := serve(key, RequirePermission("users:read")); w.Code != http.StatusForbidden {
		t.Fatalf("no longer held by owner: status = %d, want 403", w.Code)
	}
}

func TestAPIKey_RejectsOtherKeyFormats(t *testing.T) {
	setup(t, fakeSubjects{"owner": {Active: true}}, fakeKeys{owner: "owner"})

	for _, key := range []string{"wiz_live_abc12345_secret", "tbx_live_abc12345", "tbx__abc12345_secret"} {
		if w := serve(map[string]string{"X-API-Key": key}); w.Code != http.StatusUnauthorized {
			t.Errorf("key %q: status = %d, want 401", key, w.Code)
		}
	}
}

func TestClientBucket(t *testing.T) {
	tests := map[string]string{
		"203.0.113.7":             "203.0.113.7",
		"::ffff:203.0.113.7":      "203.0.113.7",
		"2001:db8:1:2:aaaa::1":    "2001:db8:1:2::/64",
		"2001:db8:1:2:bbbb::ffff": "2001:db8:1:2::/64",
		"not-an-ip":               "not-an-ip",
	}
	for ip, want := range tests {
		if got := clientBucket(ip); got != want {
			t.Errorf("clientBucket(%q) = %q, want %q", ip, got, want)
		}
	}
}

func TestAPIKey_RejectedWhenOwnerInactive(t *testing.T) {
	setup(t,
		fakeSubjects{"owner": {Active: false}},
		fakeKeys{owner: "owner", scope: []string{"roles:read"}},
	)

	w := serve(map[string]string{"X-API-Key": "tbx_live_abc12345_secret"}, RequirePermission("roles:read"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a key whose owner is deactivated", w.Code)
	}
}

func TestAPIKey_RateLimitedReturns429(t *testing.T) {
	setup(t,
		fakeSubjects{"owner": {Active: true}},
		fakeKeys{err: &ratelimit.ExceededError{RetryAfter: 1500 * time.Millisecond}},
	)

	w := serve(map[string]string{"X-API-Key": "tbx_live_abc12345_secret"})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want \"2\" (rounded up)", got)
	}
}

func TestDenyAPIKeyAuth(t *testing.T) {
	setup(t,
		fakeSubjects{"owner": {Active: true}},
		fakeKeys{owner: "owner", scope: []string{"roles:read"}},
	)

	if w := serve(map[string]string{"X-API-Key": "tbx_live_abc12345_secret"}, DenyAPIKeyAuth()); w.Code != http.StatusForbidden {
		t.Fatalf("API key: status = %d, want 403", w.Code)
	}
	if w := serve(map[string]string{"Authorization": bearer(t, "owner", false)}, DenyAPIKeyAuth()); w.Code != http.StatusOK {
		t.Fatalf("JWT: status = %d, want 200", w.Code)
	}
}

// fakeLimiter allows the first n calls per key.
type fakeLimiter struct {
	n    int
	hits map[string]int
}

func (f *fakeLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (bool, time.Duration, error) {
	f.hits[key]++
	if f.hits[key] > f.n {
		return false, 30 * time.Second, nil
	}
	return true, 0, nil
}

func TestRateLimitByIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := &fakeLimiter{n: 2, hits: map[string]int{}}
	prev := requestLimiter
	requestLimiter = limiter
	t.Cleanup(func() { requestLimiter = prev })

	r := gin.New()
	r.POST("/signin", RateLimitByIP("auth:signin", 2, time.Minute), func(c *gin.Context) { c.Status(http.StatusOK) })

	codes := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/signin", nil)
		req.RemoteAddr = "203.0.113.7:4000"
		r.ServeHTTP(w, req)
		codes = append(codes, w.Code)
	}

	if codes[0] != http.StatusOK || codes[1] != http.StatusOK || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v, want [200 200 429]", codes)
	}
	if _, ok := limiter.hits["auth:signin:203.0.113.7"]; !ok {
		t.Errorf("limiter keys = %v, want bucket auth:signin:203.0.113.7", limiter.hits)
	}
}

func TestAPIKey_RejectedKeyShowsOnlyReason(t *testing.T) {
	setup(t,
		fakeSubjects{},
		fakeKeys{err: &authz.CredentialError{Reason: "Invalid API key", Err: errors.New("API key not found")}},
	)

	w := serve(map[string]string{"X-API-Key": "tbx_live_abc12345_secret"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "Invalid API key") || strings.Contains(body, "not found") {
		t.Errorf("body = %s, want only the public reason", body)
	}
}

func TestAPIKey_InternalFailureIs500WithoutDetail(t *testing.T) {
	setup(t, fakeSubjects{}, fakeKeys{err: errors.New("dial tcp 10.0.0.5:5432: connection refused")})

	w := serve(map[string]string{"X-API-Key": "tbx_live_abc12345_secret"})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "10.0.0.5") {
		t.Errorf("body leaks the internal error: %s", w.Body.String())
	}
}

func TestJWTAuth_RejectsTokenOfEndedSession(t *testing.T) {
	setup(t, fakeSubjects{"u1": {Active: true, EndedSessions: []string{"s-ended"}}}, nil)
	token := func(sessionID string) string {
		t.Helper()
		tok, err := jwtpkg.GenerateToken("u1", "u1@example.com", "u1", "u1", false, nil, sessionID)
		if err != nil {
			t.Fatalf("GenerateToken() error = %v", err)
		}
		return "Bearer " + tok
	}

	if w := serve(map[string]string{"Authorization": token("s-ended")}); w.Code != http.StatusUnauthorized {
		t.Errorf("ended session: status = %d, want 401", w.Code)
	}
	if w := serve(map[string]string{"Authorization": token("s-live")}); w.Code != http.StatusOK {
		t.Errorf("live session: status = %d, want 200", w.Code)
	}
	// A token from before sessions had ids is only checked by the epoch.
	if w := serve(map[string]string{"Authorization": token("")}); w.Code != http.StatusOK {
		t.Errorf("token without a session: status = %d, want 200", w.Code)
	}
}
