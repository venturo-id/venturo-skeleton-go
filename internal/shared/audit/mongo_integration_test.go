package audit

// Runs only when TEST_MONGO_URI points at a MongoDB server, e.g.
//
//	TEST_MONGO_URI='mongodb://localhost:27017' go test ./internal/shared/audit/ -run Integration
//
// Each run uses, then drops, a database of its own.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestIntegration_Store(t *testing.T) {
	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI not set")
	}
	ctx := context.Background()
	cfg := Config{URI: uri, Database: "timebox_audit_test_" + uuid.NewString()[:8], Retention: time.Hour}

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Database(cfg.Database).Drop(ctx)
		_ = client.Disconnect(ctx)
	})

	log, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	actor := WithActor(WithClient(ctx, "203.0.113.7", "test-agent"), "user-1", "")
	for range 250 { // more than one batch
		log.Record(actor, Entry{Action: ActionSignIn, TargetType: TargetUser, TargetID: "user-1"})
	}
	log.Record(ctx, Entry{Action: ActionRoleCreated, ActorID: "user-2", TargetType: TargetRole, TargetID: "role-1",
		Details: map[string]any{"code": "editor"}})
	// Close writes what is still queued.
	if err := log.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Opening again with another retention updates the TTL index.
	cfg.Retention = 2 * time.Hour
	log, err = Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() again error = %v", err)
	}
	t.Cleanup(func() { _ = log.Close(ctx) })

	entries, total, err := log.List(ctx, Filter{ActorID: "user-1"}, 2, 100)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 250 || len(entries) != 100 {
		t.Fatalf("List(actor, page 2) = %d of %d, want 100 of 250", len(entries), total)
	}
	e := entries[0]
	if e.ID.IsZero() || e.IP != "203.0.113.7" || e.UserAgent != "test-agent" || e.At.IsZero() {
		t.Errorf("entry = %+v", e)
	}

	roles, total, err := log.List(ctx, Filter{TargetType: TargetRole, Action: ActionRoleCreated}, 1, 10)
	if err != nil || total != 1 || roles[0].Details["code"] != "editor" || roles[0].ActorID != "user-2" {
		t.Errorf("List(role created) = %+v, %d, %v", roles, total, err)
	}
	future := time.Now().Add(time.Hour)
	if _, total, _ := log.List(ctx, Filter{From: &future}, 1, 10); total != 0 {
		t.Errorf("List(from the future) total = %d, want 0", total)
	}
	if mine, err := log.ListByActor(ctx, "user-2", 10); err != nil || len(mine) != 1 {
		t.Errorf("ListByActor(user-2) = %d entries, %v; want 1", len(mine), err)
	}

	var ttl struct {
		Seconds int64 `bson:"expireAfterSeconds"`
	}
	cursor, err := client.Database(cfg.Database).Collection(collectionName).Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	var indexes []bson.M
	if err := cursor.All(ctx, &indexes); err != nil {
		t.Fatalf("read indexes: %v", err)
	}
	for _, index := range indexes {
		if index["name"] == ttlIndexName {
			raw, _ := bson.Marshal(index)
			_ = bson.Unmarshal(raw, &ttl)
		}
	}
	if ttl.Seconds != int64((2 * time.Hour).Seconds()) {
		t.Errorf("TTL index expires after %ds, want %ds", ttl.Seconds, int64((2 * time.Hour).Seconds()))
	}
}
