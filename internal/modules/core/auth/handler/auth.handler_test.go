package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"timebox-be/internal/middleware"
	"timebox-be/internal/modules/core/auth/service"
	jwtpkg "timebox-be/pkg/jwt"
)

const (
	testUserID    = "550e8400-e29b-41d4-a716-446655440000"
	allowedOrigin = "https://app.timebox.test"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newHandler returns a handler without a service: the requests tested
// here are answered before one is needed.
func newHandler(secure bool) *AuthHandler {
	return NewAuthHandler(nil, CookieConfig{
		Secure:         secure,
		MaxAge:         7 * 24 * time.Hour,
		AllowedOrigins: []string{allowedOrigin},
	})
}

// testContext returns a context for one request with headers, and the
// recorder its response goes to.
func testContext(headers map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/core/v1/auth/refresh", nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	return c, w
}

// refreshCookie returns the refresh cookie the response sets, or nil.
func refreshCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	var found *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == RefreshCookieName {
			if found != nil {
				t.Fatalf("the refresh cookie is set twice: %q", w.Header().Values("Set-Cookie"))
			}
			found = cookie
		}
	}
	return found
}

// decode returns the JSON envelope of a response.
func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %s", w.Body.String())
	}
	return resp
}

var web = map[string]string{ClientHeader: ClientWeb}

func TestDeliverRefreshToken(t *testing.T) {
	tests := []struct {
		name       string
		headers    map[string]string
		secure     bool
		wantBody   string
		wantCookie bool
	}{
		{"web app", web, true, "", true},
		{"web app in development", web, false, "", true},
		{"desktop app", nil, true, "refresh-token", false},
		{"another client name", map[string]string{ClientHeader: "desktop"}, true, "refresh-token", false},
		{"header value in another case", map[string]string{ClientHeader: "Web"}, true, "refresh-token", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := testContext(tt.headers)
			if got := newHandler(tt.secure).deliverRefreshToken(c, "refresh-token"); got != tt.wantBody {
				t.Errorf("token left in the body = %q, want %q", got, tt.wantBody)
			}

			cookie := refreshCookie(t, w)
			if !tt.wantCookie {
				if cookie != nil {
					t.Errorf("cookie set for a client that keeps the token itself: %+v", cookie)
				}
				return
			}
			if cookie == nil {
				t.Fatal("no refresh cookie set")
			}
			if cookie.Value != "refresh-token" || cookie.Path != RefreshCookiePath || cookie.MaxAge != 7*24*60*60 ||
				!cookie.HttpOnly || cookie.Secure != tt.secure || cookie.SameSite != http.SameSiteStrictMode {
				t.Errorf("cookie = %+v, want the token, path %s, a week, HttpOnly, Secure=%v, SameSite=Strict",
					cookie, RefreshCookiePath, tt.secure)
			}
		})
	}
}

func TestClearRefreshCookie(t *testing.T) {
	c, w := testContext(web)
	newHandler(true).clearRefreshCookie(c)
	cookie := refreshCookie(t, w)
	if cookie == nil {
		t.Fatal("the web app's cookie was not cleared")
	}
	if cookie.Value != "" || cookie.MaxAge >= 0 || cookie.Path != RefreshCookiePath ||
		!cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("clearing cookie = %+v, want empty and expired on path %s", cookie, RefreshCookiePath)
	}

	c, w = testContext(nil)
	newHandler(true).clearRefreshCookie(c)
	if got := w.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("cookie cleared for the desktop app: %q", got)
	}
}

func TestCookieRefreshToken(t *testing.T) {
	c, _ := testContext(nil)
	if got := cookieRefreshToken(c); got != "" {
		t.Errorf("no cookie: token = %q, want none", got)
	}
	c, _ = testContext(nil)
	c.Request.AddCookie(&http.Cookie{Name: RefreshCookieName, Value: "from-cookie"})
	if got := cookieRefreshToken(c); got != "from-cookie" {
		t.Errorf("token = %q, want from-cookie", got)
	}
}

func TestOriginAllowed(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{"web app on an allowed origin", map[string]string{ClientHeader: ClientWeb, "Origin": allowedOrigin}, true},
		{"web app on another origin", map[string]string{ClientHeader: ClientWeb, "Origin": "https://evil.example"}, false},
		{"origin differing in its port", map[string]string{ClientHeader: ClientWeb, "Origin": allowedOrigin + ":8443"}, false},
		{"web app without an origin", map[string]string{ClientHeader: ClientWeb}, false},
		{"allowed origin without the web header", map[string]string{"Origin": allowedOrigin}, false},
		{"nothing", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := testContext(tt.headers)
			if got := newHandler(true).originAllowed(c); got != tt.want {
				t.Errorf("originAllowed() = %v, want %v", got, tt.want)
			}
		})
	}

	// No allowed origins configured: nothing spends the cookie.
	c, _ := testContext(map[string]string{ClientHeader: ClientWeb, "Origin": allowedOrigin})
	if NewAuthHandler(nil, CookieConfig{}).originAllowed(c) {
		t.Error("originAllowed() = true with no allowed origins")
	}
}

// serveAuth runs one request through the auth routes that are answered
// before the service is needed.
func serveAuth(h *AuthHandler, req *http.Request) *httptest.ResponseRecorder {
	r := gin.New()
	r.POST("/core/v1/auth/refresh", h.Refresh)
	r.POST("/core/v1/auth/logout", func(c *gin.Context) {
		middleware.SetUserContext(c, &jwtpkg.Claims{UserID: testUserID})
	}, h.Logout)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// A refresh without a token in the body may only spend the cookie when it
// comes from the web app on an allowed origin.
func TestRefresh_CookieNeedsAllowedOrigin(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		headers     map[string]string
		cookie      bool
		wantStatus  int
		wantCode    string
		wantCleared bool
	}{
		{name: "no token, no client", wantStatus: http.StatusForbidden, wantCode: codeOriginNotAllowed},
		{name: "empty token in the body", body: `{"refresh_token": ""}`, wantStatus: http.StatusForbidden, wantCode: codeOriginNotAllowed},
		{name: "cookie without the web header", cookie: true, headers: map[string]string{"Origin": allowedOrigin},
			wantStatus: http.StatusForbidden, wantCode: codeOriginNotAllowed},
		{name: "cookie from another origin", cookie: true, headers: map[string]string{ClientHeader: ClientWeb, "Origin": "https://evil.example"},
			wantStatus: http.StatusForbidden, wantCode: codeOriginNotAllowed},
		{name: "cookie without an origin", cookie: true, headers: web,
			wantStatus: http.StatusForbidden, wantCode: codeOriginNotAllowed},
		{name: "allowed origin without a cookie", headers: map[string]string{ClientHeader: ClientWeb, "Origin": allowedOrigin},
			wantStatus: http.StatusUnauthorized, wantCode: codeSessionEnded, wantCleared: true},
		{name: "malformed body", body: `{"refresh_token": 5}`, wantStatus: http.StatusBadRequest, wantCode: "validation_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/core/v1/auth/refresh", strings.NewReader(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			if tt.cookie {
				req.AddCookie(&http.Cookie{Name: RefreshCookieName, Value: "from-cookie"})
			}

			w := serveAuth(newHandler(true), req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tt.wantStatus, w.Body.String())
			}
			if resp := decode(t, w); resp["code"] != tt.wantCode {
				t.Errorf("code = %v, want %s", resp["code"], tt.wantCode)
			}
			cookie := refreshCookie(t, w)
			if cleared := cookie != nil && cookie.MaxAge < 0; cleared != tt.wantCleared {
				t.Errorf("cookie cleared = %v, want %v", cleared, tt.wantCleared)
			}
		})
	}
}

// A logout with no refresh token and an access token from before sessions
// has nothing to end; the web app's cookie is cleared all the same.
func TestLogout_NothingToEnd(t *testing.T) {
	for _, tt := range []struct {
		name        string
		headers     map[string]string
		wantCleared bool
	}{
		{"web app", web, true},
		{"desktop app", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/core/v1/auth/logout", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			w := serveAuth(newHandler(true), req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (%s)", w.Code, w.Body.String())
			}
			cookie := refreshCookie(t, w)
			if cleared := cookie != nil && cookie.MaxAge < 0; cleared != tt.wantCleared {
				t.Errorf("cookie cleared = %v, want %v", cleared, tt.wantCleared)
			}
		})
	}
}

// handle answers err the way an endpoint would, and returns the response.
func handle(err error) *httptest.ResponseRecorder {
	c, w := testContext(nil)
	newHandler(true).handleError(c, err, "Failed to do the thing")
	return w
}

// Every error the service names is answered with its status and code,
// also when wrapped.
func TestHandleError_KnownErrors(t *testing.T) {
	for _, known := range knownErrors {
		t.Run(known.code+"/"+known.err.Error(), func(t *testing.T) {
			w := handle(fmt.Errorf("doing the thing: %w", known.err))
			if w.Code != known.status {
				t.Fatalf("status = %d, want %d", w.Code, known.status)
			}
			resp := decode(t, w)
			if resp["code"] != known.code || resp["message"] != known.message || resp["data"] != nil {
				t.Errorf("response = %v, want code %s, message %q and no data", resp, known.code, known.message)
			}
			errs, _ := resp["errors"].(map[string]any)
			if detail, _ := errs["detail"].([]any); (known.detail == "") != (len(detail) == 0) ||
				(known.detail != "" && detail[0] != known.detail) {
				t.Errorf("errors = %v, want detail %q", resp["errors"], known.detail)
			}
		})
	}
}

func TestHandleError_WithData(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantData   map[string]any // nil: data is null
		wantRetry  string         // the Retry-After header and retry_after; "" for none
	}{
		{
			name: "wrong code", err: &service.CodeError{Err: service.ErrInvalidCode, AttemptsLeft: 3},
			wantStatus: http.StatusBadRequest, wantCode: codeInvalidCode, wantData: map[string]any{"attempts_left": float64(3)},
		},
		{
			name: "wrong code, wrapped", err: fmt.Errorf("verify: %w", &service.CodeError{Err: service.ErrInvalidCode, AttemptsLeft: 1}),
			wantStatus: http.StatusBadRequest, wantCode: codeInvalidCode, wantData: map[string]any{"attempts_left": float64(1)},
		},
		{
			name: "too many wrong codes", err: &service.CodeError{Err: service.ErrTooManyAttempts, RetryAfter: 41200 * time.Millisecond},
			wantStatus: http.StatusTooManyRequests, wantCode: codeTooManyAttempts, wantRetry: "42",
		},
		{
			name: "too many wrong codes, new one available now", err: &service.CodeError{Err: service.ErrTooManyAttempts},
			wantStatus: http.StatusTooManyRequests, wantCode: codeTooManyAttempts, wantRetry: "1",
		},
		{
			name: "expired code", err: &service.CodeError{Err: service.ErrCodeExpired},
			wantStatus: http.StatusBadRequest, wantCode: codeCodeExpired,
		},
		{
			name: "resend cooldown", err: &service.CooldownError{RetryAfter: 30 * time.Second},
			wantStatus: http.StatusTooManyRequests, wantCode: codeResendCooldown, wantRetry: "30",
		},
		{
			name: "email quota", err: &service.EmailQuotaError{RetryAfter: time.Hour},
			wantStatus: http.StatusTooManyRequests, wantCode: codeEmailQuotaExceeded, wantRetry: "3600",
		},
		{
			name: "unverified email at sign-in", err: &service.EmailNotVerifiedError{Email: "ada@timebox.test"},
			wantStatus: http.StatusForbidden, wantCode: codeEmailNotVerified, wantData: map[string]any{"email": "ada@timebox.test"},
		},
		{
			name: "unverified email at refresh", err: service.ErrEmailNotVerified,
			wantStatus: http.StatusForbidden, wantCode: codeEmailNotVerified,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := handle(tt.err)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tt.wantStatus, w.Body.String())
			}
			resp := decode(t, w)
			if resp["code"] != tt.wantCode {
				t.Errorf("code = %v, want %s", resp["code"], tt.wantCode)
			}

			data, _ := resp["data"].(map[string]any)
			if (tt.wantData == nil) != (resp["data"] == nil) || len(data) != len(tt.wantData) {
				t.Errorf("data = %v, want %v", resp["data"], tt.wantData)
			}
			for k, want := range tt.wantData {
				if data[k] != want {
					t.Errorf("data.%s = %v, want %v", k, data[k], want)
				}
			}

			if got := w.Header().Get("Retry-After"); got != tt.wantRetry {
				t.Errorf("Retry-After = %q, want %q", got, tt.wantRetry)
			}
			retry, present := resp["retry_after"]
			if (tt.wantRetry != "") != present || (present && fmt.Sprint(retry) != tt.wantRetry) {
				t.Errorf("retry_after = %v, want %q", retry, tt.wantRetry)
			}
		})
	}
}

// Anything unexpected is a 500 that says only what failed, never why.
func TestHandleError_Unexpected(t *testing.T) {
	secret := "connect to db.internal:5432 as timebox: password authentication failed"
	w := handle(fmt.Errorf("find user: %w", errors.New(secret)))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	resp := decode(t, w)
	if resp["code"] != "internal_error" || resp["message"] != "Failed to do the thing" {
		t.Errorf("response = %v, want internal_error with the fallback message", resp)
	}
	if body := w.Body.String(); strings.Contains(body, "db.internal") || strings.Contains(body, "find user") {
		t.Errorf("the error leaked into the response: %s", body)
	}
}
