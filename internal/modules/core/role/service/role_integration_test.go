package service

// Integration test for a role update racing another one. It needs a
// migrated PostgreSQL database and runs only when TEST_DATABASE_URL points
// at one (see the auth service's account_integration_test.go).

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"timebox-be/internal/modules/core/role/domain"
	"timebox-be/internal/modules/core/role/dto"
	"timebox-be/internal/modules/core/role/repository"
	"timebox-be/internal/shared/audit"
	"timebox-be/internal/shared/authz"
)

type noCache struct{}

func (noCache) InvalidateAll(context.Context) error { return nil }

// Renaming a role while someone narrows its permissions keeps the
// narrowed permissions: an update writes only what it changes.
func TestIntegration_RoleUpdateKeepsConcurrentChange(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	repo := repository.NewRoleRepository(pool)
	now := time.Now()
	role := &domain.Role{
		ID: uuid.NewString(), Code: "it_" + uuid.NewString()[:8], Name: "Integration",
		Permissions: domain.Permissions{"users": "admin"}, IsActive: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.Create(ctx, role); err != nil {
		t.Fatalf("create role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM core.roles WHERE id = $1`, role.ID)
	})

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE core.roles SET permissions = '{"users": "viewer"}' WHERE id = $1`, role.ID); err != nil {
		t.Fatalf("narrow permissions: %v", err)
	}

	svc := NewRoleService(repo, noCache{}, audit.Discard{})
	updated, updateErr := make(chan struct{}), error(nil)
	go func() {
		defer close(updated)
		name := "Renamed"
		_, updateErr = svc.Update(ctx, role.ID, &dto.UpdateRoleRequest{Name: &name}, uuid.NewString(), authz.Subject{Active: true, SuperAdmin: true})
	}()
	waitForLockWait(t, pool, updated)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	<-updated
	if updateErr != nil {
		t.Fatalf("Update() error = %v", updateErr)
	}

	after, err := repo.FindByID(ctx, role.ID)
	if err != nil || after == nil {
		t.Fatalf("FindByID() = %v, %v", after, err)
	}
	if after.Name != "Renamed" {
		t.Errorf("name = %s, want Renamed", after.Name)
	}
	if after.Permissions["users"] != "viewer" {
		t.Errorf("permissions = %v: the rename wrote back the permissions it read", after.Permissions)
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
