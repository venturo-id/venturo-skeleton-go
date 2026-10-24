// Package googleauth signs users in with Google directly, as an OAuth 2.0
// client: the app runs the authorization code flow with PKCE, and the
// backend — which holds the client secret — exchanges the code for an ID
// token and verifies it.
//
// There is one OAuth client per platform: a "Desktop app" client for the
// Electron app, which receives the code on a loopback redirect
// (http://127.0.0.1:<port>/...), and a "Web application" client for the
// browser, whose redirect URIs are a fixed allowlist.
package googleauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"google.golang.org/api/idtoken"
)

// Platforms a client is configured for.
const (
	PlatformDesktop = "desktop"
	PlatformWeb     = "web"
)

// tokenURL is Google's OAuth 2.0 token endpoint.
const tokenURL = "https://oauth2.googleapis.com/token"

// exchangeTimeout bounds the code exchange, ID token verification
// included (it may fetch Google's signing keys).
const exchangeTimeout = 10 * time.Second

// maxTokenResponse caps the token endpoint's answer; it is a few KB.
const maxTokenResponse = 1 << 20

// issuers are the values Google puts in an ID token's "iss" claim.
var issuers = []string{"accounts.google.com", "https://accounts.google.com"}

var (
	// ErrNotConfigured: the platform has no OAuth client.
	ErrNotConfigured = errors.New("google sign-in is not configured for this platform")
	// ErrInvalidRedirectURI: the redirect URI is not one the platform's
	// client may use.
	ErrInvalidRedirectURI = errors.New("redirect uri is not allowed")
	// ErrExchangeFailed: Google refused the code, or could not be reached.
	ErrExchangeFailed = errors.New("google code exchange failed")
	// ErrInvalidIDToken: the ID token Google returned did not verify.
	ErrInvalidIDToken = errors.New("invalid google id token")
)

// Client is the OAuth client of one platform. The desktop secret is not
// confidential (it ships in the app's traffic to Google), but Google
// still requires it for the exchange.
type Client struct {
	ID     string
	Secret string
}

// Enabled reports whether the client is set.
func (c Client) Enabled() bool {
	return c.ID != "" && c.Secret != ""
}

// Config holds the OAuth clients.
type Config struct {
	Desktop Client
	Web     Client
	// WebRedirectURIs are the redirect URIs the web client may use, exactly
	// as registered with Google (e.g. https://app.timebox.id/oauth/google).
	WebRedirectURIs []string
}

// Validate names the first inconsistent setting. Secrets are never echoed.
func (c Config) Validate() error {
	for _, p := range []struct {
		name   string
		client Client
	}{{"GOOGLE_DESKTOP", c.Desktop}, {"GOOGLE_WEB", c.Web}} {
		if (p.client.ID == "") != (p.client.Secret == "") {
			return fmt.Errorf("%s_CLIENT_ID and %s_CLIENT_SECRET must be set together", p.name, p.name)
		}
	}
	if c.Web.Enabled() && len(c.WebRedirectURIs) == 0 {
		return errors.New("GOOGLE_WEB_REDIRECT_URIS is required with the web client")
	}
	for _, raw := range c.WebRedirectURIs {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" ||
			!(u.Scheme == "https" || (u.Scheme == "http" && isLoopbackHost(u.Hostname()))) {
			return fmt.Errorf("GOOGLE_WEB_REDIRECT_URIS: %q must be an https URL (http only for localhost)", raw)
		}
	}
	return nil
}

// Exchange is what the app sends back after Google redirected to it.
type Exchange struct {
	Platform     string
	Code         string
	CodeVerifier string
	RedirectURI  string
}

// Identity is the verified Google account behind an exchanged code.
type Identity struct {
	// Subject is Google's stable id of the account ("sub"): it survives a
	// change of the account's email.
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
	// IssuedAt is when Google issued the ID token, i.e. moments ago: a
	// code is single use and short lived.
	IssuedAt time.Time
	// Claims is the full claim set, for user_identities.raw_profile.
	Claims map[string]any
}

// Verifier exchanges authorization codes and verifies the ID tokens. It is
// safe for concurrent use.
type Verifier struct {
	cfg      Config
	http     *http.Client
	tokenURL string
	validate func(ctx context.Context, idToken, audience string) (*idtoken.Payload, error)
}

// New builds a Verifier. It does not contact Google.
func New(ctx context.Context, cfg Config) (*Verifier, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	validator, err := idtoken.NewValidator(ctx)
	if err != nil {
		return nil, fmt.Errorf("googleauth: id token validator: %w", err)
	}
	return &Verifier{
		cfg:      cfg,
		http:     &http.Client{Timeout: exchangeTimeout},
		tokenURL: tokenURL,
		validate: validator.Validate,
	}, nil
}

// client returns the platform's OAuth client.
func (v *Verifier) client(platform string) (Client, error) {
	var c Client
	switch platform {
	case PlatformDesktop:
		c = v.cfg.Desktop
	case PlatformWeb:
		c = v.cfg.Web
	}
	if !c.Enabled() {
		return Client{}, ErrNotConfigured
	}
	return c, nil
}

// ClientID returns the public client id the app starts the flow with.
func (v *Verifier) ClientID(platform string) (string, error) {
	c, err := v.client(platform)
	if err != nil {
		return "", err
	}
	return c.ID, nil
}

// RedirectURIs returns the redirect URIs the platform may use; nil for the
// desktop client, which redirects to any loopback port.
func (v *Verifier) RedirectURIs(platform string) []string {
	if platform == PlatformWeb {
		return slices.Clone(v.cfg.WebRedirectURIs)
	}
	return nil
}

// checkRedirectURI enforces the platform's redirect rule before anything
// is sent to Google. Google checks it too: the exchange fails unless the
// URI matches the one the code was issued for.
func (v *Verifier) checkRedirectURI(platform, raw string) error {
	if platform == PlatformWeb {
		if slices.Contains(v.cfg.WebRedirectURIs, raw) {
			return nil
		}
		return ErrInvalidRedirectURI
	}

	// Desktop: a loopback IP on any port. "localhost" is left out, as
	// Google recommends: it may resolve to something else.
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" || u.Port() == "" {
		return ErrInvalidRedirectURI
	}
	if host := u.Hostname(); host != "127.0.0.1" && host != "::1" {
		return ErrInvalidRedirectURI
	}
	return nil
}

// tokenResponse is the part of the token endpoint's answer used here.
type tokenResponse struct {
	IDToken          string `json:"id_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Exchange trades an authorization code for the Google account it was
// issued to. The ID token comes straight from Google over TLS and is
// verified anyway: signature, audience (the platform's client), issuer and
// expiry.
func (v *Verifier) Exchange(ctx context.Context, e Exchange) (*Identity, error) {
	client, err := v.client(e.Platform)
	if err != nil {
		return nil, err
	}
	if err := v.checkRedirectURI(e.Platform, e.RedirectURI); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {e.Code},
		"code_verifier": {e.CodeVerifier},
		"redirect_uri":  {e.RedirectURI},
		"client_id":     {client.ID},
		"client_secret": {client.Secret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrExchangeFailed, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := v.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrExchangeFailed, err)
	}
	defer resp.Body.Close()

	var tok tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxTokenResponse)).Decode(&tok); err != nil {
		return nil, fmt.Errorf("%w: status %d, unreadable answer", ErrExchangeFailed, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || tok.IDToken == "" {
		// e.g. invalid_grant: the code is used, expired, or was issued for
		// another redirect URI or code challenge.
		return nil, fmt.Errorf("%w: status %d, %s: %s", ErrExchangeFailed, resp.StatusCode, tok.Error, tok.ErrorDescription)
	}

	return v.verify(ctx, tok.IDToken, client.ID)
}

// verify checks an ID token issued to clientID and reads the account.
func (v *Verifier) verify(ctx context.Context, rawToken, clientID string) (*Identity, error) {
	payload, err := v.validate(ctx, rawToken, clientID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidIDToken, err)
	}
	if !slices.Contains(issuers, payload.Issuer) {
		return nil, fmt.Errorf("%w: unexpected issuer %q", ErrInvalidIDToken, payload.Issuer)
	}
	if payload.Subject == "" {
		return nil, fmt.Errorf("%w: no subject", ErrInvalidIDToken)
	}

	claims := payload.Claims
	identity := &Identity{
		Subject:       payload.Subject,
		Email:         stringClaim(claims, "email"),
		EmailVerified: boolClaim(claims, "email_verified"),
		Name:          strings.TrimSpace(stringClaim(claims, "name")),
		Picture:       stringClaim(claims, "picture"),
		IssuedAt:      time.Unix(payload.IssuedAt, 0),
		Claims:        claims,
	}
	return identity, nil
}

func stringClaim(claims map[string]any, name string) string {
	s, _ := claims[name].(string)
	return s
}

// boolClaim reads a boolean claim, which some Google tokens have carried
// as the string "true".
func boolClaim(claims map[string]any, name string) bool {
	switch v := claims[name].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

// isLoopbackHost reports whether host names this machine.
func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
