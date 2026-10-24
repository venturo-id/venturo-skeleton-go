package storage

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testSecret = "test-secret-access-key"

func validConfig() Config {
	return Config{
		AccountID:       "abc123def456",
		AccessKeyID:     "test-access-key-id",
		SecretAccessKey: testSecret,
		Bucket:          "timebox-test",
	}
}

func newTestR2(t *testing.T, publicURL string) *R2 {
	t.Helper()
	cfg := validConfig()
	cfg.PublicURL = publicURL
	r, err := NewR2(cfg)
	if err != nil {
		t.Fatalf("NewR2() error = %v", err)
	}
	return r
}

func TestConfigEnabled(t *testing.T) {
	if !validConfig().Enabled() {
		t.Error("Enabled() = false for a complete config")
	}
	if (Config{}).Enabled() {
		t.Error("Enabled() = true for an empty config")
	}

	cfg := validConfig()
	cfg.Bucket = ""
	if cfg.Enabled() {
		t.Error("Enabled() = true without a bucket")
	}

	cfg = validConfig()
	cfg.PublicURL = ""
	if !cfg.Enabled() {
		t.Error("Enabled() = false without the optional PublicURL")
	}
}

func TestNewR2_Validation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string // substring; "" means success
	}{
		{name: "valid", mutate: func(*Config) {}},
		{name: "valid with public url", mutate: func(c *Config) { c.PublicURL = "https://cdn.example.com/" }},
		{name: "missing account id", mutate: func(c *Config) { c.AccountID = "" }, wantErr: "AccountID is required"},
		{name: "missing access key id", mutate: func(c *Config) { c.AccessKeyID = "" }, wantErr: "AccessKeyID is required"},
		{name: "missing secret", mutate: func(c *Config) { c.SecretAccessKey = "" }, wantErr: "SecretAccessKey is required"},
		{name: "missing bucket", mutate: func(c *Config) { c.Bucket = "" }, wantErr: "Bucket is required"},
		{name: "account id with host characters", mutate: func(c *Config) { c.AccountID = "evil.com/x" }, wantErr: "AccountID must contain only letters and digits"},
		{name: "relative public url", mutate: func(c *Config) { c.PublicURL = "cdn.example.com" }, wantErr: "PublicURL must be an absolute http(s) URL"},
		{name: "non-http public url", mutate: func(c *Config) { c.PublicURL = "ftp://cdn.example.com" }, wantErr: "PublicURL must be an absolute http(s) URL"},
		{name: "public url with query", mutate: func(c *Config) { c.PublicURL = "https://cdn.example.com/?v=1" }, wantErr: "PublicURL must be an absolute http(s) URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)

			r, err := NewR2(cfg)
			if tt.wantErr == "" {
				if err != nil || r == nil {
					t.Fatalf("NewR2() = %v, %v; want a client", r, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("NewR2() error = nil, want %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewR2() error = %q, want it to contain %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), testSecret) {
				t.Errorf("NewR2() error leaks the secret: %q", err)
			}
		})
	}
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		key     string
		wantErr bool
	}{
		{"avatars/user-1/photo.png", false},
		{"file.txt", false},
		{"a/b..c/d", false}, // ".." inside a segment is not traversal
		{"..hidden", false},
		{"", true},
		{"/avatars/photo.png", true},
		{"..", true},
		{"../secret", true},
		{"avatars/../secret", true},
		{"avatars/..", true},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			err := ValidateKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateKey(%q) error = %v, wantErr %v", tt.key, err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidKey) {
				t.Errorf("ValidateKey(%q) error = %v, want ErrInvalidKey", tt.key, err)
			}
		})
	}
}

func TestR2_RejectsInvalidKeys(t *testing.T) {
	r := newTestR2(t, "https://cdn.example.com")
	ctx := context.Background()

	for _, key := range []string{"", "/abs", "a/../b"} {
		if err := r.Upload(ctx, key, strings.NewReader("x"), "text/plain"); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Upload(%q) error = %v, want ErrInvalidKey", key, err)
		}
		if err := r.Delete(ctx, key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Delete(%q) error = %v, want ErrInvalidKey", key, err)
		}
		if _, err := r.PresignPut(ctx, key, "text/plain", time.Minute); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("PresignPut(%q) error = %v, want ErrInvalidKey", key, err)
		}
		if _, err := r.PresignGet(ctx, key, time.Minute); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("PresignGet(%q) error = %v, want ErrInvalidKey", key, err)
		}
		if got := r.PublicURL(key); got != "" {
			t.Errorf("PublicURL(%q) = %q, want empty", key, got)
		}
	}
}

func TestR2_PublicURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		key  string
		want string
	}{
		{"not configured", "", "a.png", ""},
		{"plain", "https://cdn.example.com", "avatars/a.png", "https://cdn.example.com/avatars/a.png"},
		{"trailing slash", "https://cdn.example.com/", "avatars/a.png", "https://cdn.example.com/avatars/a.png"},
		{"several trailing slashes", "https://cdn.example.com//", "a.png", "https://cdn.example.com/a.png"},
		{"base with path", "https://pub-123.r2.dev/files/", "a.png", "https://pub-123.r2.dev/files/a.png"},
		{"space and unicode", "https://cdn.example.com", "docs/my file ü.pdf", "https://cdn.example.com/docs/my%20file%20%C3%BC.pdf"},
		{"reserved characters", "https://cdn.example.com", "a+b/c?d#e%f.png", "https://cdn.example.com/a%2Bb/c%3Fd%23e%25f.png"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestR2(t, tt.base)
			if got := r.PublicURL(tt.key); got != tt.want {
				t.Errorf("PublicURL(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestR2_Presign(t *testing.T) {
	r := newTestR2(t, "")
	ctx := context.Background()
	const key = "avatars/user 1/photo.png"

	tests := []struct {
		name    string
		presign func() (string, error)
	}{
		{"put", func() (string, error) { return r.PresignPut(ctx, key, "image/png", 15*time.Minute) }},
		{"get", func() (string, error) { return r.PresignGet(ctx, key, 15*time.Minute) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := tt.presign()
			if err != nil {
				t.Fatalf("presign error = %v", err)
			}
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("presigned URL %q does not parse: %v", raw, err)
			}

			if u.Scheme != "https" {
				t.Errorf("scheme = %q, want https", u.Scheme)
			}
			if want := "abc123def456.r2.cloudflarestorage.com"; u.Host != want {
				t.Errorf("host = %q, want %q", u.Host, want)
			}
			if want := "/timebox-test/" + key; u.Path != want {
				t.Errorf("path = %q, want %q", u.Path, want)
			}
			q := u.Query()
			if q.Get("X-Amz-Signature") == "" {
				t.Error("URL has no X-Amz-Signature")
			}
			if got := q.Get("X-Amz-Expires"); got != "900" {
				t.Errorf("X-Amz-Expires = %q, want 900", got)
			}
			if got := q.Get("X-Amz-Credential"); !strings.Contains(got, "/auto/s3/") {
				t.Errorf("X-Amz-Credential = %q, want it scoped to region auto", got)
			}
			if strings.Contains(raw, testSecret) {
				t.Error("presigned URL contains the secret access key")
			}
		})
	}
}

func TestR2_PresignPutSignsContentType(t *testing.T) {
	r := newTestR2(t, "")
	raw, err := r.PresignPut(context.Background(), "a.png", "image/png", time.Minute)
	if err != nil {
		t.Fatalf("PresignPut() error = %v", err)
	}
	u, _ := url.Parse(raw)
	if got := u.Query().Get("X-Amz-SignedHeaders"); !strings.Contains(got, "content-type") {
		t.Errorf("X-Amz-SignedHeaders = %q, want content-type signed", got)
	}

	if _, err := r.PresignPut(context.Background(), "a.png", "", time.Minute); err == nil {
		t.Error("PresignPut() without a content type: error = nil")
	}
}

func TestR2_PresignTTL(t *testing.T) {
	r := newTestR2(t, "")
	ctx := context.Background()

	for _, ttl := range []time.Duration{0, -time.Minute, time.Millisecond, 7*24*time.Hour + time.Second} {
		if _, err := r.PresignGet(ctx, "a.png", ttl); err == nil {
			t.Errorf("PresignGet(ttl=%s) error = nil, want an error", ttl)
		}
		if _, err := r.PresignPut(ctx, "a.png", "image/png", ttl); err == nil {
			t.Errorf("PresignPut(ttl=%s) error = nil, want an error", ttl)
		}
	}

	if _, err := r.PresignGet(ctx, "a.png", 7*24*time.Hour); err != nil {
		t.Errorf("PresignGet(ttl=7d) error = %v", err)
	}
}
