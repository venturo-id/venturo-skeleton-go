package avatar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// googleTimeout bounds a whole download, redirects and body included.
	googleTimeout = 5 * time.Second
	// maxGoogleBytes caps a downloaded picture.
	maxGoogleBytes = 5 << 20 // 5 MiB
	maxRedirects   = 3
)

// errPictureURL is returned for a picture URL FetchGooglePicture refuses to
// download, before anything is requested.
var errPictureURL = errors.New("avatar: not a Google profile picture URL")

// googleSize is the size option Google puts at the end of a profile
// picture URL, e.g. ".../a/ACg8oc...=s96-c" (96 pixels, cropped).
var googleSize = regexp.MustCompile(`=s\d+(-c)?$`)

// allowedHost reports whether pictures may be downloaded from host (a URL's
// host, with any port). A variable so tests can allow an httptest server.
var allowedHost = isGoogleUserContent

// googleTransport sends the downloads; nil is http.DefaultTransport. A
// variable so tests can trust an httptest server's certificate.
var googleTransport http.RoundTripper

// isGoogleUserContent reports whether host is googleusercontent.com or one
// of its subdomains, without a port.
func isGoogleUserContent(host string) bool {
	host = strings.ToLower(host)
	return host == "googleusercontent.com" || strings.HasSuffix(host, ".googleusercontent.com")
}

// checkPictureURL lets through only https URLs on an allowed host.
func checkPictureURL(u *url.URL) error {
	if u.Scheme != "https" || u.User != nil || !allowedHost(u.Host) {
		return errPictureURL
	}
	return nil
}

// FetchGooglePicture downloads the Google profile picture at rawURL (the
// "picture" claim of a Google ID token) and returns it normalized (see
// Normalize). Only https URLs on googleusercontent.com are fetched, after
// every redirect too, and the picture is asked for at MaxSide pixels when
// the URL carries Google's size option. The download is bounded to 5
// seconds and 5 MiB.
func FetchGooglePicture(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errPictureURL
	}
	if err := checkPictureURL(u); err != nil {
		return nil, err
	}
	u.Path = googleSize.ReplaceAllString(u.Path, fmt.Sprintf("=s%d$1", MaxSide))
	u.RawPath = ""

	client := &http.Client{
		Timeout:   googleTimeout,
		Transport: googleTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return errors.New("avatar: too many redirects")
			}
			return checkPictureURL(req.URL)
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("avatar: build request: %w", err)
	}
	req.Header.Set("Accept", "image/webp,image/png,image/jpeg")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("avatar: download picture: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("avatar: download picture: status %d", resp.StatusCode)
	}
	if resp.ContentLength > maxGoogleBytes {
		return nil, fmt.Errorf("avatar: picture is larger than %d bytes", maxGoogleBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxGoogleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("avatar: read picture: %w", err)
	}
	if len(data) > maxGoogleBytes {
		return nil, fmt.Errorf("avatar: picture is larger than %d bytes", maxGoogleBytes)
	}

	return Normalize(data)
}
