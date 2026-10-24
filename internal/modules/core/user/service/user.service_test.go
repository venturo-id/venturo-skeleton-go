package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"

	roleDomain "timebox-be/internal/modules/core/role/domain"
	"timebox-be/internal/shared/authz"
	"timebox-be/pkg/avatar"
)

// fakeRoles is an in-memory RoleLookup.
type fakeRoles struct {
	byID      map[string]*roleDomain.Role
	userRoles map[string][]roleDomain.Role
}

func (f fakeRoles) FindByID(_ context.Context, id string) (*roleDomain.Role, error) {
	return f.byID[id], nil
}

func (f fakeRoles) GetUserRoles(_ context.Context, userID string) ([]roleDomain.Role, error) {
	return f.userRoles[userID], nil
}

func (fakeRoles) GetRoleNamesByUserIDs(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (fakeRoles) AssignRoleToUser(context.Context, string, string, string) error {
	return nil
}

func (fakeRoles) ReplaceUserRoles(context.Context, string, string, string) error {
	return nil
}

var (
	superAdminRole = roleDomain.Role{ID: "r-super", Code: "super_admin", IsActive: true, Permissions: roleDomain.Permissions{}}
	adminRole      = roleDomain.Role{ID: "r-admin", Code: "administrator", IsActive: true, Permissions: roleDomain.Permissions{
		"roles": "admin", "users": "admin",
	}}
	userManagerRole = roleDomain.Role{ID: "r-um", Code: "user_manager", IsActive: true, Permissions: roleDomain.Permissions{
		"users": "editor",
	}}
	viewerRole = roleDomain.Role{ID: "r-viewer", Code: "viewer", IsActive: true, Permissions: roleDomain.Permissions{
		"users": "viewer",
	}}
)

func newTestService() *UserService {
	return &UserService{roleLookup: fakeRoles{
		byID: map[string]*roleDomain.Role{
			superAdminRole.ID:  &superAdminRole,
			adminRole.ID:       &adminRole,
			userManagerRole.ID: &userManagerRole,
			viewerRole.ID:      &viewerRole,
		},
		userRoles: map[string][]roleDomain.Role{
			"u-super":   {superAdminRole},
			"u-admin":   {adminRole},
			"u-manager": {userManagerRole},
			"u-viewer":  {viewerRole},
		},
	}}
}

// userManager holds users at editor level and nothing else.
var userManager = authz.Subject{Active: true, Permissions: roleDomain.Permissions{"users": "editor"}.ToStringList()}

func TestValidateRole_Ceiling(t *testing.T) {
	s := newTestService()
	ctx := context.Background()
	id := func(r roleDomain.Role) *string { return &r.ID }

	tests := []struct {
		name   string
		roleID *string
		caller authz.Subject
		want   error
	}{
		{"no role change", nil, userManager, nil},
		{"role within the caller's permissions", id(viewerRole), userManager, nil},
		{"role equal to the caller's", id(userManagerRole), userManager, nil},
		{"administrator from a user manager", id(adminRole), userManager, ErrRoleNotAllowed},
		{"super_admin from a non super admin", id(superAdminRole), userManager, ErrRoleNotAllowed},
		{"super_admin from a super admin", id(superAdminRole), authz.Subject{Active: true, SuperAdmin: true}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.validateRole(ctx, tt.roleID, tt.caller); !errors.Is(err, tt.want) {
				t.Errorf("validateRole() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestGuardTarget(t *testing.T) {
	s := newTestService()
	ctx := context.Background()

	tests := []struct {
		name   string
		target string
		caller authz.Subject
		want   error
	}{
		{"user holding less", "u-viewer", userManager, nil},
		{"user holding the same", "u-manager", userManager, nil},
		{"user without roles", "u-none", userManager, nil},
		{"administrator from a user manager", "u-admin", userManager, ErrCannotManageHigherUser},
		{"super admin from a non super admin", "u-super", userManager, ErrCannotManageSuperAdmin},
		{"anyone from a super admin", "u-super", authz.Subject{Active: true, SuperAdmin: true}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.guardTarget(ctx, tt.target, tt.caller); !errors.Is(err, tt.want) {
				t.Errorf("guardTarget() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// memObjects is an in-memory bucket served from https://cdn.timebox.test.
type memObjects struct {
	objects map[string][]byte
	deleted []string
}

const testAvatarBase = "https://cdn.timebox.test"

func newAvatarStore() (*avatar.Store, *memObjects) {
	objects := &memObjects{objects: map[string][]byte{}}
	return avatar.NewStore(objects, testAvatarBase), objects
}

func (m *memObjects) Upload(_ context.Context, key string, body io.Reader, _ string) error {
	data, err := io.ReadAll(body)
	m.objects[key] = data
	return err
}

func (m *memObjects) Delete(_ context.Context, key string) error {
	m.deleted = append(m.deleted, key)
	delete(m.objects, key)
	return nil
}

func (m *memObjects) PublicURL(key string) string { return testAvatarBase + "/" + key }

// testPNG returns a small opaque PNG.
func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	img.Set(3, 3, color.NRGBA{R: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return buf.Bytes()
}

func TestAvatar_WithoutStorage(t *testing.T) {
	s := newTestService()
	ctx := context.Background()

	if _, err := s.UploadAvatar(ctx, "u-viewer", testPNG(t)); !errors.Is(err, ErrStorageNotConfigured) {
		t.Errorf("UploadAvatar() error = %v, want ErrStorageNotConfigured", err)
	}
	if _, err := s.RemoveAvatar(ctx, "u-viewer"); !errors.Is(err, ErrStorageNotConfigured) {
		t.Errorf("RemoveAvatar() error = %v, want ErrStorageNotConfigured", err)
	}
}

// An upload that is not an image is refused before anything is stored.
func TestUploadAvatar_InvalidImage(t *testing.T) {
	s := newTestService()
	store, objects := newAvatarStore()
	s.avatars = store

	for _, data := range [][]byte{nil, []byte("GIF89a not really"), testPNG(t)[:30]} {
		if _, err := s.UploadAvatar(context.Background(), "u-viewer", data); !errors.Is(err, ErrInvalidImage) {
			t.Errorf("UploadAvatar(%q) error = %v, want ErrInvalidImage", data, err)
		}
	}
	if len(objects.objects) != 0 {
		t.Errorf("objects stored for an invalid image: %d", len(objects.objects))
	}
}
