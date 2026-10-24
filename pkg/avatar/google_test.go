package avatar

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
)

// allowServer lets FetchGooglePicture download from srv (and only srv)
// until the test ends.
func allowServer(t *testing.T, srv *httptest.Server) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("server URL: %v", err)
	}
	prevHost, prevTransport := allowedHost, googleTransport
	allowedHost = func(host string) bool { return host == u.Host }
	googleTransport = srv.Client().Transport
	t.Cleanup(func() { allowedHost, googleTransport = prevHost, prevTransport })
}

func TestIsGoogleUserContent(t *testing.T) {
	allowed := []string{"googleusercontent.com", "lh3.googleusercontent.com", "LH3.GoogleUserContent.com"}
	for _, host := range allowed {
		if !isGoogleUserContent(host) {
			t.Errorf("isGoogleUserContent(%q) = false, want true", host)
		}
	}

	refused := []string{
		"", "google.com", "evilgoogleusercontent.com", "googleusercontent.com.evil.com",
		"lh3.googleusercontent.com:8443", "lh3.googleusercontent.com.",
	}
	for _, host := range refused {
		if isGoogleUserContent(host) {
			t.Errorf("isGoogleUserContent(%q) = true, want false", host)
		}
	}
}

func TestFetchGooglePicture_RefusesURLs(t *testing.T) {
	urls := []string{
		"",
		"::not a url",
		"http://lh3.googleusercontent.com/a/ACg8oc=s96-c",
		"https://example.com/a.jpg",
		"https://googleusercontent.com.example.com/a.jpg",
		"https://lh3.googleusercontent.com:8443/a/ACg8oc=s96-c",
		"https://user@lh3.googleusercontent.com/a/ACg8oc=s96-c",
		"file:///etc/passwd",
	}

	for _, raw := range urls {
		if _, err := FetchGooglePicture(context.Background(), raw); !errors.Is(err, errPictureURL) {
			t.Errorf("FetchGooglePicture(%q) error = %v, want errPictureURL", raw, err)
		}
	}
}

func TestFetchGooglePicture_RequestsFullSize(t *testing.T) {
	picture := encodePNG(t, solid(96, 96, color.NRGBA{R: 40, G: 120, B: 220, A: 255}))
	var path atomic.Value
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(picture)
	}))
	defer srv.Close()
	allowServer(t, srv)

	tests := []struct{ path, want string }{
		{"/a/ACg8ocK=s96-c", "/a/ACg8ocK=s512-c"},
		{"/a/ACg8ocK=s96", "/a/ACg8ocK=s512"},
		{"/a/ACg8ocK", "/a/ACg8ocK"},
		{"/a/s96-c/photo.jpg", "/a/s96-c/photo.jpg"},
	}

	for _, tt := range tests {
		out, err := FetchGooglePicture(context.Background(), srv.URL+tt.path)
		if err != nil {
			t.Fatalf("FetchGooglePicture(%s) error = %v", tt.path, err)
		}
		if got := path.Load(); got != tt.want {
			t.Errorf("FetchGooglePicture(%s) requested %v, want %s", tt.path, got, tt.want)
		}
		if b := decodeJPEG(t, out).Bounds(); b.Dx() != 96 || b.Dy() != 96 {
			t.Errorf("picture size = %dx%d, want 96x96", b.Dx(), b.Dy())
		}
	}
}

func TestFetchGooglePicture_ChecksRedirects(t *testing.T) {
	var elsewhereHits atomic.Int32
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhereHits.Add(1)
	}))
	defer elsewhere.Close()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/internal", http.StatusFound)
	}))
	defer srv.Close()
	allowServer(t, srv)

	if _, err := FetchGooglePicture(context.Background(), srv.URL+"/a/x=s96-c"); !errors.Is(err, errPictureURL) {
		t.Errorf("FetchGooglePicture() error = %v, want errPictureURL", err)
	}
	if n := elsewhereHits.Load(); n != 0 {
		t.Errorf("the redirect target was requested %d time(s)", n)
	}
}

func TestFetchGooglePicture_Refuses(t *testing.T) {
	tooLarge := bytes.Repeat([]byte{0}, maxGoogleBytes+1)

	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"not found", func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}},
		{"larger than the cap, announced", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(len(tooLarge)))
			_, _ = w.Write(tooLarge)
		}},
		{"larger than the cap, streamed", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(tooLarge[:1024])
			w.(http.Flusher).Flush() // no Content-Length from here on
			_, _ = w.Write(tooLarge[1024:])
		}},
		{"not an image", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("<html>sign in</html>"))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(tt.handler)
			defer srv.Close()
			allowServer(t, srv)

			out, err := FetchGooglePicture(context.Background(), srv.URL+"/a/x=s96-c")
			if err == nil {
				t.Fatalf("FetchGooglePicture() = %d bytes, want an error", len(out))
			}
		})
	}
}
