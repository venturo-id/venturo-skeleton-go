package avatar

import (
	"context"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

const testUserID = "550e8400-e29b-41d4-a716-446655440000"

// fakeObjects is an in-memory bucket served from base, building URLs the
// way storage.R2 does.
type fakeObjects struct {
	base      string
	objects   map[string][]byte
	types     map[string]string
	deleted   []string
	uploadErr error
}

func newFakeObjects(base string) *fakeObjects {
	return &fakeObjects{base: base, objects: map[string][]byte{}, types: map[string]string{}}
}

func (f *fakeObjects) Upload(_ context.Context, key string, body io.Reader, contentType string) error {
	if f.uploadErr != nil {
		return f.uploadErr
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	f.objects[key], f.types[key] = data, contentType
	return nil
}

func (f *fakeObjects) Delete(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	delete(f.objects, key)
	return nil
}

func (f *fakeObjects) PublicURL(key string) string {
	if f.base == "" {
		return ""
	}
	segments := strings.Split(key, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.TrimRight(f.base, "/") + "/" + strings.Join(segments, "/")
}

func TestStore_Put(t *testing.T) {
	objects := newFakeObjects("https://cdn.example.com/")
	store := NewStore(objects, "https://cdn.example.com/")
	ctx := context.Background()

	first, err := store.Put(ctx, testUserID, []byte("jpeg-1"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	pattern := regexp.MustCompile(`^https://cdn\.example\.com/avatars/` + testUserID + `/[0-9a-f-]{36}\.jpg$`)
	if !pattern.MatchString(first) {
		t.Errorf("Put() url = %q, want <base>/avatars/<user_id>/<uuid>.jpg", first)
	}

	key := strings.TrimPrefix(first, "https://cdn.example.com/")
	if string(objects.objects[key]) != "jpeg-1" {
		t.Errorf("object %q = %q, want the uploaded bytes", key, objects.objects[key])
	}
	if objects.types[key] != "image/jpeg" {
		t.Errorf("content type = %q, want image/jpeg", objects.types[key])
	}

	// Every upload gets a key of its own.
	second, err := store.Put(ctx, testUserID, []byte("jpeg-2"))
	if err != nil {
		t.Fatalf("second Put() error = %v", err)
	}
	if second == first {
		t.Error("two uploads got the same URL")
	}
}

func TestStore_PutRefuses(t *testing.T) {
	ctx := context.Background()

	if _, err := NewStore(newFakeObjects("https://cdn.example.com"), "https://cdn.example.com").Put(ctx, "../other", []byte("x")); err == nil {
		t.Error("Put() with a user id that is not a UUID: error = nil")
	}

	unserved := newFakeObjects("")
	if _, err := NewStore(unserved, "").Put(ctx, testUserID, []byte("x")); err == nil {
		t.Error("Put() on a bucket without a public URL: error = nil")
	}
	if len(unserved.objects) != 0 {
		t.Error("Put() uploaded to a bucket without a public URL")
	}

	failing := newFakeObjects("https://cdn.example.com")
	failing.uploadErr = errors.New("r2 down")
	if _, err := NewStore(failing, "https://cdn.example.com").Put(ctx, testUserID, []byte("x")); !errors.Is(err, failing.uploadErr) {
		t.Errorf("Put() error = %v, want the upload's", err)
	}
}

func TestStore_Delete(t *testing.T) {
	const base = "https://cdn.example.com/files"

	tests := []struct {
		name    string
		url     string
		wantKey string // "" means nothing is deleted
	}{
		{"own avatar", base + "/avatars/" + testUserID + "/0b6f.jpg", "avatars/" + testUserID + "/0b6f.jpg"},
		{"escaped segment", base + "/avatars/" + testUserID + "/a%20b.jpg", "avatars/" + testUserID + "/a b.jpg"},
		{"google picture", "https://lh3.googleusercontent.com/a/ACg8oc=s96-c", ""},
		{"other path on the same origin", base + "/documents/" + testUserID + "/a.jpg", ""},
		{"base without its path", "https://cdn.example.com/avatars/" + testUserID + "/a.jpg", ""},
		{"origin that merely starts the same", "https://cdn.example.com/files.evil.com/avatars/a.jpg", ""},
		{"dot-dot segment", base + "/avatars/../secret.txt", ""},
		{"escaped dot-dot segment", base + "/avatars/%2e%2e/secret.txt", ""},
		{"escaped slash", base + "/avatars/a%2F..%2Fsecret.txt", ""},
		{"query", base + "/avatars/" + testUserID + "/a.jpg?x=1", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := newFakeObjects(base)
			if err := NewStore(objects, base+"/").Delete(context.Background(), tt.url); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			switch {
			case tt.wantKey == "" && len(objects.deleted) != 0:
				t.Errorf("Delete() deleted %q, want nothing", objects.deleted)
			case tt.wantKey != "" && (len(objects.deleted) != 1 || objects.deleted[0] != tt.wantKey):
				t.Errorf("Delete() deleted %q, want [%s]", objects.deleted, tt.wantKey)
			}
		})
	}
}

func TestStore_DeleteWhatPutStored(t *testing.T) {
	objects := newFakeObjects("https://pub-123.r2.dev")
	store := NewStore(objects, "https://pub-123.r2.dev")
	ctx := context.Background()

	avatarURL, err := store.Put(ctx, testUserID, []byte("jpeg"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if err := store.Delete(ctx, avatarURL); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(objects.objects) != 0 {
		t.Errorf("objects left after Delete: %v", objects.objects)
	}
}

func TestStore_DeleteWithoutPublicURL(t *testing.T) {
	objects := newFakeObjects("")
	if err := NewStore(objects, "").Delete(context.Background(), "/avatars/"+testUserID+"/a.jpg"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(objects.deleted) != 0 {
		t.Errorf("Delete() deleted %q without a public URL", objects.deleted)
	}
}
