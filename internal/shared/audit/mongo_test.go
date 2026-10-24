package audit

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// unreachableStore returns a Store on a MongoDB that is never there:
// writes fail fast, which is all these tests need.
func unreachableStore(t *testing.T) *Store {
	t.Helper()
	client, err := mongo.Connect(options.Client().
		ApplyURI("mongodb://127.0.0.1:1").
		SetServerSelectionTimeout(50 * time.Millisecond))
	if err != nil {
		t.Fatalf("mongo.Connect() error = %v", err)
	}
	return newStore(client, client.Database("timebox_test").Collection(collectionName))
}

func closeCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestStore_RecordAfterCloseIsDropped(t *testing.T) {
	s := unreachableStore(t)
	if err := s.Close(closeCtx(t)); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	for range 3 {
		s.Record(context.Background(), Entry{Action: ActionSignIn})
	}
	if n := len(s.queue); n != 0 {
		t.Errorf("%d entries queued after Close, want none: nothing writes them", n)
	}
	if n := s.droppedClosed.Load(); n != 3 {
		t.Errorf("dropped after Close = %d, want 3", n)
	}

	// Close is idempotent
	if err := s.Close(closeCtx(t)); err != nil {
		t.Errorf("second Close() error = %v", err)
	}
}

// No entry recorded concurrently with Close is left stranded in the queue.
func TestStore_ConcurrentRecordAndClose(t *testing.T) {
	s := unreachableStore(t)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				s.Record(context.Background(), Entry{Action: ActionSignIn})
			}
		})
	}
	wg.Go(func() {
		if err := s.Close(closeCtx(t)); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	wg.Go(func() {
		if err := s.Close(closeCtx(t)); err != nil {
			t.Errorf("concurrent Close() error = %v", err)
		}
	})
	wg.Wait()

	if n := len(s.queue); n != 0 {
		t.Errorf("%d entries stranded in the queue after Close", n)
	}
}
