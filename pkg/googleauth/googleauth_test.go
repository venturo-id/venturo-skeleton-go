package googleauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/idtoken"
)

const (
	desktopID = "desktop-id.apps.googleusercontent.com"
	webID     = "web-id.apps.googleusercontent.com"
)

func testConfig() Config {
	return Config{
		Desktop:         Client{ID: desktopID, Secret: "desktop-secret"},
		Web:             Client{ID: webID, Secret: "web-secret"},
		WebRedirectURIs: []string{"http://localhost:5173/oauth/google", "https://app.timebox.id/oauth/google"},
	}
}

// newTestVerifier points the verifier at a fake token endpoint that
// answers with idToken (or status/body when status is not 200), and
// accepts any ID token as payload.
func newTestVerifier(t *testing.T, status int, body string, payload *idtoken.Payload) (*Verifier, *http.Request) {
	t.Helper()
	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		got = *r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return &Verifier{
		cfg:      testConfig(),
		http:     srv.Client(),
		tokenURL: srv.URL,
		validate: func(_ context.Context, idToken, audience string) (*idtoken.Payload, error) {
			if idToken != "the-id-token" {
				return nil, errors.New("bad token")
			}
			if audience != payload.Audience {
				return nil, errors.New("audience mismatch")
			}
			return payload, nil
		},
	}, &got
}

func googlePayload(aud string) *idtoken.Payload {
	return &idtoken.Payload{
		Issuer:   "https://accounts.google.com",
		Audience: aud,
		Subject:  "1234567890",
		IssuedAt: 1_800_000_000,
		Claims: map[string]any{
			"email":          "Ana@Example.com",
			"email_verified": true,
			"name":           " Ana ",
			"picture":        "https://lh3.googleusercontent.com/a/x=s96-c",
		},
	}
}

func TestExchange_Desktop(t *testing.T) {
	v, got := newTestVerifier(t, http.StatusOK, `{"id_token":"the-id-token","access_token":"x"}`, googlePayload(desktopID))

	identity, err := v.Exchange(context.Background(), Exchange{
		Platform: PlatformDesktop, Code: "the-code", CodeVerifier: "the-verifier", RedirectURI: "http://127.0.0.1:53682/callback",
	})
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if identity.Subject != "1234567890" || identity.Email != "Ana@Example.com" || !identity.EmailVerified || identity.Name != "Ana" {
		t.Errorf("identity = %+v", identity)
	}

	form := got.PostForm
	want := map[string]string{
		"grant_type": "authorization_code", "code": "the-code", "code_verifier": "the-verifier",
		"redirect_uri": "http://127.0.0.1:53682/callback", "client_id": desktopID, "client_secret": "desktop-secret",
	}
	for k, v := range want {
		if form.Get(k) != v {
			t.Errorf("form %s = %q, want %q", k, form.Get(k), v)
		}
	}
}

func TestExchange_WebUsesWebClient(t *testing.T) {
	v, got := newTestVerifier(t, http.StatusOK, `{"id_token":"the-id-token"}`, googlePayload(webID))

	_, err := v.Exchange(context.Background(), Exchange{
		Platform: PlatformWeb, Code: "c", CodeVerifier: "v", RedirectURI: "https://app.timebox.id/oauth/google",
	})
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if got.PostForm.Get("client_id") != webID || got.PostForm.Get("client_secret") != "web-secret" {
		t.Errorf("client = %q", got.PostForm.Get("client_id"))
	}
}

func TestExchange_RedirectURIRules(t *testing.T) {
	v, _ := newTestVerifier(t, http.StatusOK, `{"id_token":"the-id-token"}`, googlePayload(desktopID))

	cases := []struct {
		platform, uri string
		ok            bool
	}{
		{PlatformDesktop, "http://127.0.0.1:8123/cb", true},
		{PlatformDesktop, "http://[::1]:8123/cb", true},
		{PlatformDesktop, "http://localhost:8123/cb", false},
		{PlatformDesktop, "http://127.0.0.1/cb", false},
		{PlatformDesktop, "https://127.0.0.1:8123/cb", false},
		{PlatformDesktop, "http://evil.example:8123/cb", false},
		{PlatformDesktop, "http://user@127.0.0.1:8123/cb", false},
		{PlatformWeb, "https://app.timebox.id/oauth/google", true},
		{PlatformWeb, "https://app.timebox.id/oauth/google/", false},
		{PlatformWeb, "https://evil.example/oauth/google", false},
	}
	for _, tc := range cases {
		err := v.checkRedirectURI(tc.platform, tc.uri)
		if (err == nil) != tc.ok {
			t.Errorf("%s %s: err = %v, want ok=%v", tc.platform, tc.uri, err, tc.ok)
		}
	}
}

func TestExchange_NotConfigured(t *testing.T) {
	v, _ := newTestVerifier(t, http.StatusOK, `{}`, googlePayload(desktopID))
	v.cfg.Web = Client{}

	if _, err := v.ClientID(PlatformWeb); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("ClientID(web) err = %v, want ErrNotConfigured", err)
	}
	if _, err := v.ClientID("ios"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("ClientID(ios) err = %v, want ErrNotConfigured", err)
	}
	if id, err := v.ClientID(PlatformDesktop); err != nil || id != desktopID {
		t.Errorf("ClientID(desktop) = %q, %v", id, err)
	}
}

func TestExchange_GoogleRefusesCode(t *testing.T) {
	v, _ := newTestVerifier(t, http.StatusBadRequest, `{"error":"invalid_grant","error_description":"Bad Request"}`, googlePayload(desktopID))

	_, err := v.Exchange(context.Background(), Exchange{
		Platform: PlatformDesktop, Code: "used", CodeVerifier: "v", RedirectURI: "http://127.0.0.1:1/cb",
	})
	if !errors.Is(err, ErrExchangeFailed) {
		t.Fatalf("err = %v, want ErrExchangeFailed", err)
	}
}

func TestExchange_RejectsForeignIssuerAndAudience(t *testing.T) {
	payload := googlePayload(desktopID)
	payload.Issuer = "https://evil.example"
	v, _ := newTestVerifier(t, http.StatusOK, `{"id_token":"the-id-token"}`, payload)

	_, err := v.Exchange(context.Background(), Exchange{
		Platform: PlatformDesktop, Code: "c", CodeVerifier: "v", RedirectURI: "http://127.0.0.1:1/cb",
	})
	if !errors.Is(err, ErrInvalidIDToken) {
		t.Fatalf("foreign issuer: err = %v, want ErrInvalidIDToken", err)
	}

	// An ID token issued to the web client is not accepted for desktop.
	v, _ = newTestVerifier(t, http.StatusOK, `{"id_token":"the-id-token"}`, googlePayload(webID))
	_, err = v.Exchange(context.Background(), Exchange{
		Platform: PlatformDesktop, Code: "c", CodeVerifier: "v", RedirectURI: "http://127.0.0.1:1/cb",
	})
	if !errors.Is(err, ErrInvalidIDToken) {
		t.Fatalf("foreign audience: err = %v, want ErrInvalidIDToken", err)
	}
}

func TestBoolClaim_AcceptsStringTrue(t *testing.T) {
	if !boolClaim(map[string]any{"email_verified": "true"}, "email_verified") {
		t.Error(`"true" should read as true`)
	}
	if boolClaim(map[string]any{"email_verified": "false"}, "email_verified") {
		t.Error(`"false" should read as false`)
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"empty", Config{}, true},
		{"full", testConfig(), true},
		{"id without secret", Config{Desktop: Client{ID: "x"}}, false},
		{"web without redirects", Config{Web: Client{ID: "x", Secret: "y"}}, false},
		{"http redirect off localhost", Config{Web: Client{ID: "x", Secret: "y"}, WebRedirectURIs: []string{"http://app.timebox.id/cb"}}, false},
	}
	for _, tc := range cases {
		if err := tc.cfg.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}
