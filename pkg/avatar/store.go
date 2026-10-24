package avatar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// keyPrefix is where avatars live in the bucket: avatars/<user_id>/<id>.jpg.
const keyPrefix = "avatars/"

// Objects is the part of object storage a Store uses. *storage.R2
// implements it.
type Objects interface {
	Upload(ctx context.Context, key string, body io.Reader, contentType string) error
	Delete(ctx context.Context, key string) error
	PublicURL(key string) string
}

// Store keeps avatars in a publicly served bucket. Every upload gets a key
// of its own, so an avatar URL never changes content and can be cached
// forever. Callers treat a nil *Store as "storage not configured" and never
// call its methods.
type Store struct {
	objects Objects
	base    string // the public URL the bucket is served from, without trailing "/"
}

// NewStore returns a Store keeping avatars in objects. publicURL is the
// base URL the bucket is served from — the one objects.PublicURL builds
// on — and tells the avatars this Store owns from any other URL.
func NewStore(objects Objects, publicURL string) *Store {
	return &Store{objects: objects, base: strings.TrimRight(publicURL, "/")}
}

// Put stores jpeg (an image from Normalize) as a new avatar of userID and
// returns its public URL: <publicURL>/avatars/<userID>/<random id>.jpg.
func (s *Store) Put(ctx context.Context, userID string, jpeg []byte) (string, error) {
	if err := uuid.Validate(userID); err != nil {
		return "", fmt.Errorf("avatar: user id must be a UUID: %w", err)
	}

	key := keyPrefix + userID + "/" + uuid.NewString() + ".jpg"
	avatarURL := s.objects.PublicURL(key)
	if avatarURL == "" {
		return "", errors.New("avatar: the bucket has no public URL")
	}
	if err := s.objects.Upload(ctx, key, bytes.NewReader(jpeg), "image/jpeg"); err != nil {
		return "", err
	}
	return avatarURL, nil
}

// Delete removes the avatar at avatarURL when it is one of this Store's —
// under <publicURL>/avatars/. Any other URL, such as an avatar set before
// uploads existed, is not ours to delete: Delete then does nothing and
// returns nil.
func (s *Store) Delete(ctx context.Context, avatarURL string) error {
	key, ok := s.key(avatarURL)
	if !ok {
		return nil
	}
	return s.objects.Delete(ctx, key)
}

// key derives the object key back from a URL Put returned. It reports
// false for a URL outside <publicURL>/avatars/, or one whose path could
// not have come from Put (a query, an empty or dot segment, an escaped
// "/").
func (s *Store) key(avatarURL string) (string, bool) {
	if s.base == "" {
		return "", false
	}
	path, ok := strings.CutPrefix(avatarURL, s.base+"/")
	if !ok || !strings.HasPrefix(path, keyPrefix) || strings.ContainsAny(path, "?#") {
		return "", false
	}

	segments := strings.Split(path, "/")
	for i, escaped := range segments {
		segment, err := url.PathUnescape(escaped)
		if err != nil || segment == "" || segment == "." || segment == ".." || strings.Contains(segment, "/") {
			return "", false
		}
		segments[i] = segment
	}
	return strings.Join(segments, "/"), true
}
