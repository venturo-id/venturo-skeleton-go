package handler

import (
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
)

// The browser app keeps its refresh token in an httpOnly cookie, out of
// reach of scripts; the desktop app keeps it itself and sends it in the
// body. A request says it comes from the browser app with ClientHeader.
const (
	// ClientHeader names the client a request comes from.
	ClientHeader = "X-Timebox-Client"
	// ClientWeb is ClientHeader's value for the browser app.
	ClientWeb = "web"

	// RefreshCookieName holds the browser app's refresh token. Its path
	// limits it to the /auth endpoints that read it.
	RefreshCookieName = "tb_refresh"
	RefreshCookiePath = "/core/v1/auth"
)

// CookieConfig is how the refresh cookie is set.
type CookieConfig struct {
	// Secure marks the cookie HTTPS-only; off only for local development
	// over plain http.
	Secure bool
	// MaxAge is the refresh token's lifetime.
	MaxAge time.Duration
	// AllowedOrigins are the browser origins (CORS_ALLOWED_ORIGINS) a
	// refresh may come from with the cookie.
	AllowedOrigins []string
}

// isWebClient reports whether the request comes from the browser app.
func isWebClient(c *gin.Context) bool {
	return c.GetHeader(ClientHeader) == ClientWeb
}

// setRefreshCookie hands the browser app its refresh token.
func (h *AuthHandler) setRefreshCookie(c *gin.Context, refreshToken string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    refreshToken,
		Path:     RefreshCookiePath,
		MaxAge:   int(h.cookie.MaxAge.Seconds()),
		HttpOnly: true,
		Secure:   h.cookie.Secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearRefreshCookie removes the browser app's refresh token.
func (h *AuthHandler) clearRefreshCookie(c *gin.Context) {
	if !isWebClient(c) {
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     RefreshCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cookie.Secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// deliverRefreshToken moves a new refresh token into the cookie for the
// browser app, and returns what stays in the body: nothing for the
// browser app, the token for anyone else.
func (h *AuthHandler) deliverRefreshToken(c *gin.Context, refreshToken string) string {
	if !isWebClient(c) {
		return refreshToken
	}
	h.setRefreshCookie(c, refreshToken)
	return ""
}

// cookieRefreshToken returns the browser app's refresh token from its
// cookie, or "" when there is none.
func cookieRefreshToken(c *gin.Context) string {
	value, err := c.Cookie(RefreshCookieName)
	if err != nil {
		return ""
	}
	return value
}

// originAllowed reports whether a request that spends the cookie came
// from the browser app on an allowed origin. SameSite=Strict already keeps
// other sites from sending the cookie; this also holds when the browser
// does not enforce it.
func (h *AuthHandler) originAllowed(c *gin.Context) bool {
	origin := c.GetHeader("Origin")
	return isWebClient(c) && origin != "" && slices.Contains(h.cookie.AllowedOrigins, origin)
}
