// Package storage is the object storage abstraction. Modules depend on the
// Storage interface; R2 (Cloudflare R2 through its S3-compatible API) is the
// implementation, and tests can substitute a fake.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ErrInvalidKey is returned for an object key that ValidateKey rejects.
var ErrInvalidKey = errors.New("storage: invalid object key")

// Storage is the abstraction over object storage.
type Storage interface {
	// Upload stores body under key, replacing any existing object.
	Upload(ctx context.Context, key string, body io.Reader, contentType string) error

	// Delete removes the object at key. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error

	// PresignPut returns a URL a client can PUT the object to directly,
	// valid for ttl. The upload must send contentType as its Content-Type.
	PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error)

	// PresignGet returns a time-limited URL to download the object.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)

	// PublicURL returns the permanent public URL of the object, or "" when
	// the bucket is not publicly served.
	PublicURL(key string) string
}

// ValidateKey rejects object keys that are empty, start with "/" or contain
// a ".." path segment, so a key built from user input cannot escape the
// prefix it was meant to live under. Keys are "/"-separated paths such as
// "avatars/<user_id>/<uuid>.png".
func ValidateKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: key is empty", ErrInvalidKey)
	}
	if strings.HasPrefix(key, "/") {
		return fmt.Errorf("%w: key must not start with \"/\"", ErrInvalidKey)
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == ".." {
			return fmt.Errorf("%w: key must not contain a \"..\" segment", ErrInvalidKey)
		}
	}
	return nil
}
