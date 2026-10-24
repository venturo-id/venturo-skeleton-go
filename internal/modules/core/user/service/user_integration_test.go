package service

// Integration tests for what the user service does against the database:
// what a caller may not do to their own account here, updates racing
// another change of the same user, and avatars. They need a migrated PostgreSQL
// database and run only when TEST_DATABASE_URL points at one (see the auth
// service's account_integration_test.go).

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	roleRepo "timebox-be/internal/modules/core/role/repository"
	"timebox-be/internal/modules/core/user/domain"
	"timebox-be/internal/modules/core/user/dto"
	"timebox-be/internal/modules/core/user/repository"
	"timebox-be/internal/shared/audit"
	"timebox-be/internal/shared/authz"
)

type noSessions struct{}

func (noSessions) RevokeAllSessions(context.Context, string) error              { return nil }
func (noSessions) ChangePassword(context.Context, string, string, string) error { return nil }

type noCache struct{}

func (noCache) InvalidateUser(context.Context, string) error { return nil }

var superAdmin = authz.Subject{Active: true, SuperAdmin: true}

func newIntegrationService(t *testing.T) (*UserService, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	svc := NewUserService(pool, repository.NewUserRepository(pool), roleRepo.NewRoleRepository(pool),
		noCache{}, noSessions{}, audit.Discard{}, nil)
	return svc, pool
}

// createUser stores an active, verified user and schedules its deletion.
func createUser(t *testing.T, pool *pgxpool.Pool) *domain.User {
	t.Helper()
	now := time.Now()
	name := "it-" + uuid.NewString()[:12]
	user := &domain.User{
		ID: uuid.NewString(), Email: name + "@timebox.test", Username: name,
		IsActive: true, CreatedAt: now, UpdatedAt: now,
	}
	user.MarkEmailVerified(true, now)
	if err := repository.NewUserRepository(pool).Create(context.Background(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM core.users WHERE id = $1`, user.ID)
	})
	return user
}

func ptr[T any](v T) *T { return &v }

// Your own email, status and existence are not changed through
// /users/:id: that needs only a session, /account asks for the password.
func TestIntegration_SelfManagement(t *testing.T) {
	svc, pool := newIntegrationService(t)
	ctx := context.Background()
	user := createUser(t, pool)

	if _, err := svc.Update(ctx, user.ID, &dto.UpdateUserRequest{Email: ptr("it-" + uuid.NewString()[:12] + "@timebox.test")}, user.ID, superAdmin); !errors.Is(err, ErrChangeOwnEmail) {
		t.Errorf("own email: err = %v, want ErrChangeOwnEmail", err)
	}
	if _, err := svc.Update(ctx, user.ID, &dto.UpdateUserRequest{IsActive: ptr(false)}, user.ID, superAdmin); !errors.Is(err, ErrCannotRemoveSelf) {
		t.Errorf("deactivate yourself: err = %v, want ErrCannotRemoveSelf", err)
	}
	if err := svc.Delete(ctx, user.ID, user.ID, superAdmin); !errors.Is(err, ErrCannotRemoveSelf) {
		t.Errorf("delete yourself: err = %v, want ErrCannotRemoveSelf", err)
	}
	// The rest of your own row stays editable, the address included as is.
	if _, err := svc.Update(ctx, user.ID, &dto.UpdateUserRequest{Email: ptr(user.Email), FullName: ptr("Own Name")}, user.ID, superAdmin); err != nil {
		t.Errorf("own name: err = %v", err)
	}

	// Someone else may move the address; they vouch for it.
	moved := "it-" + uuid.NewString()[:12] + "@timebox.test"
	resp, err := svc.Update(ctx, user.ID, &dto.UpdateUserRequest{Email: ptr(moved)}, uuid.NewString(), superAdmin)
	if err != nil {
		t.Fatalf("admin moves the address: err = %v", err)
	}
	if resp.Email != moved || !resp.IsEmailVerified {
		t.Errorf("after the admin's change: email = %s verified = %v", resp.Email, resp.IsEmailVerified)
	}
}

// An update waits for a concurrent change of the same user and does not
// write back what it read before: a deactivation committed meanwhile
// stands.
func TestIntegration_UpdateKeepsConcurrentChange(t *testing.T) {
	svc, pool := newIntegrationService(t)
	ctx := context.Background()
	user := createUser(t, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE core.users SET is_active = FALSE WHERE id = $1`, user.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	updated, updateErr := make(chan struct{}), error(nil)
	go func() {
		defer close(updated)
		_, updateErr = svc.Update(ctx, user.ID, &dto.UpdateUserRequest{FullName: ptr("Renamed")}, uuid.NewString(), superAdmin)
	}()
	waitForLockWait(t, pool, updated)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	<-updated
	if updateErr != nil {
		t.Fatalf("Update() error = %v", updateErr)
	}

	after, err := repository.NewUserRepository(pool).FindByID(ctx, user.ID)
	if err != nil || after == nil {
		t.Fatalf("FindByID() = %v, %v", after, err)
	}
	if after.IsActive {
		t.Error("the update reactivated a user deactivated meanwhile")
	}
	if after.FullName == nil || *after.FullName != "Renamed" {
		t.Errorf("full_name = %v, want Renamed", after.FullName)
	}
}

// An upload stores a new picture and deletes the one it replaces, unless
// that one is not in the bucket; removing deletes the last one.
func TestIntegration_Avatar(t *testing.T) {
	svc, pool := newIntegrationService(t)
	store, objects := newAvatarStore()
	svc.avatars = store
	ctx := context.Background()
	user := createUser(t, pool)

	// An avatar from before uploads, e.g. copied from Google, is left alone.
	const legacy = "https://lh3.googleusercontent.com/a/ACg8oc=s96-c"
	if _, err := pool.Exec(ctx, `UPDATE core.users SET avatar_url = $2 WHERE id = $1`, user.ID, legacy); err != nil {
		t.Fatalf("set legacy avatar: %v", err)
	}

	first, err := svc.UploadAvatar(ctx, user.ID, testPNG(t))
	if err != nil {
		t.Fatalf("UploadAvatar() error = %v", err)
	}
	prefix := testAvatarBase + "/avatars/" + user.ID + "/"
	if first.AvatarURL == nil || !strings.HasPrefix(*first.AvatarURL, prefix) || !strings.HasSuffix(*first.AvatarURL, ".jpg") {
		t.Fatalf("avatar_url = %v, want %s<id>.jpg", first.AvatarURL, prefix)
	}
	if len(objects.objects) != 1 || len(objects.deleted) != 0 {
		t.Errorf("after the first upload: %d object(s), deleted %q", len(objects.objects), objects.deleted)
	}

	second, err := svc.UploadAvatar(ctx, user.ID, testPNG(t))
	if err != nil {
		t.Fatalf("second UploadAvatar() error = %v", err)
	}
	if *second.AvatarURL == *first.AvatarURL {
		t.Error("the second upload kept the first URL")
	}
	firstKey := strings.TrimPrefix(*first.AvatarURL, testAvatarBase+"/")
	if _, ok := objects.objects[firstKey]; ok || len(objects.objects) != 1 {
		t.Errorf("after the second upload: %d object(s), the first one deleted: %v", len(objects.objects), !ok)
	}

	removed, err := svc.RemoveAvatar(ctx, user.ID)
	if err != nil {
		t.Fatalf("RemoveAvatar() error = %v", err)
	}
	if removed.AvatarURL != nil {
		t.Errorf("avatar_url after remove = %s, want none", *removed.AvatarURL)
	}
	if len(objects.objects) != 0 {
		t.Errorf("%d object(s) left after remove", len(objects.objects))
	}
}

// An upload for a user deleted meanwhile leaves no picture behind.
func TestIntegration_AvatarOfDeletedUser(t *testing.T) {
	svc, pool := newIntegrationService(t)
	store, objects := newAvatarStore()
	svc.avatars = store
	ctx := context.Background()
	user := createUser(t, pool)
	if err := repository.NewUserRepository(pool).SoftDelete(ctx, user.ID, user.ID); err != nil {
		t.Fatalf("SoftDelete() error = %v", err)
	}

	if _, err := svc.UploadAvatar(ctx, user.ID, testPNG(t)); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("UploadAvatar() error = %v, want ErrUserNotFound", err)
	}
	if len(objects.objects) != 0 || len(objects.deleted) != 1 {
		t.Errorf("objects left: %d, deleted: %q; want the upload deleted again", len(objects.objects), objects.deleted)
	}
	if _, err := svc.RemoveAvatar(ctx, user.ID); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("RemoveAvatar() error = %v, want ErrUserNotFound", err)
	}
}

// waitForLockWait returns once a session of the test database waits for a
// lock, or done is closed.
func waitForLockWait(t *testing.T, pool *pgxpool.Pool, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return
		default:
		}
		var waiting int
		err := pool.QueryRow(context.Background(), `
			SELECT COUNT(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()
		`).Scan(&waiting)
		if err != nil {
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the update neither waited for a lock nor finished")
}
