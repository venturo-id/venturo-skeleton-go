package audit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"timebox-be/pkg/logger"
)

// Config selects the MongoDB database the log is kept in.
type Config struct {
	// URI is the MongoDB connection string; empty disables the log.
	URI string
	// Database holds the audit_logs collection. Default "timebox".
	Database string
	// Retention is how long an entry is kept (a TTL index). Default one year.
	Retention time.Duration
}

// Defaults for the optional Config fields.
const (
	DefaultDatabase  = "timebox"
	DefaultRetention = 365 * 24 * time.Hour
)

// Enabled reports whether a MongoDB URI is set.
func (c Config) Enabled() bool { return c.URI != "" }

const (
	collectionName = "audit_logs"
	ttlIndexName   = "at_ttl"

	// queueSize entries wait for MongoDB before new ones are dropped.
	queueSize = 4096
	// batchSize entries are written at a time, at least every flushEvery.
	batchSize  = 100
	flushEvery = time.Second

	connectTimeout = 5 * time.Second
	writeTimeout   = 10 * time.Second
)

// Store is the MongoDB-backed log. Record queues the entry; a single
// background writer inserts them in batches. When MongoDB is down or slow
// the queue fills and further entries are dropped — and logged as an
// error — rather than slowing requests down. Once Close has begun, entries
// are dropped too: nothing would write them.
type Store struct {
	client *mongo.Client
	coll   *mongo.Collection

	queue chan Entry
	// mu orders Record against Close: an entry queued under the read lock
	// is in the queue before closing closes, so the writer's final drain
	// sees it.
	mu      sync.RWMutex
	closed  bool
	closing chan struct{}
	stopped chan struct{}

	closeOnce      sync.Once
	disconnectOnce sync.Once
	disconnectErr  error

	dropped       atomic.Int64
	droppedClosed atomic.Int64
}

var _ Log = (*Store)(nil)

// Open connects to the log: a Store when cfg.URI is set, Discard
// otherwise. With a URI, MongoDB must answer and the indexes must be in
// place, or Open fails.
func Open(ctx context.Context, cfg Config) (Log, error) {
	if !cfg.Enabled() {
		return Discard{}, nil
	}
	if cfg.Database == "" {
		cfg.Database = DefaultDatabase
	}
	if cfg.Retention <= 0 {
		cfg.Retention = DefaultRetention
	}

	client, err := mongo.Connect(options.Client().
		ApplyURI(cfg.URI).
		SetAppName("timebox-api").
		SetServerSelectionTimeout(connectTimeout))
	if err != nil {
		return nil, fmt.Errorf("audit: connect to MongoDB: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("audit: MongoDB did not answer: %w", err)
	}

	db := client.Database(cfg.Database)
	if err := ensureIndexes(ctx, db, cfg.Retention); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}

	return newStore(client, db.Collection(collectionName)), nil
}

// newStore starts the background writer of a Store on coll.
func newStore(client *mongo.Client, coll *mongo.Collection) *Store {
	s := &Store{
		client:  client,
		coll:    coll,
		queue:   make(chan Entry, queueSize),
		closing: make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go s.run()
	return s
}

// ensureIndexes creates the indexes the reads use, and the TTL index that
// expires entries after retention — updating it when retention changed.
func ensureIndexes(ctx context.Context, db *mongo.Database, retention time.Duration) error {
	coll := db.Collection(collectionName)
	_, err := coll.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "actor_id", Value: 1}, {Key: "at", Value: -1}}},
		{Keys: bson.D{{Key: "target_type", Value: 1}, {Key: "target_id", Value: 1}, {Key: "at", Value: -1}}},
		{Keys: bson.D{{Key: "action", Value: 1}, {Key: "at", Value: -1}}},
	})
	if err != nil {
		return fmt.Errorf("audit: create indexes: %w", err)
	}

	seconds := int32(retention / time.Second)
	_, err = coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "at", Value: 1}},
		Options: options.Index().SetName(ttlIndexName).SetExpireAfterSeconds(seconds),
	})
	var cmdErr mongo.CommandError
	if errors.As(err, &cmdErr) && cmdErr.Code == 85 { // IndexOptionsConflict: retention changed
		err = db.RunCommand(ctx, bson.D{
			{Key: "collMod", Value: collectionName},
			{Key: "index", Value: bson.D{{Key: "name", Value: ttlIndexName}, {Key: "expireAfterSeconds", Value: seconds}}},
		}).Err()
	}
	if err != nil {
		return fmt.Errorf("audit: create TTL index: %w", err)
	}
	return nil
}

// Record queues e. It never blocks: with the queue full, or once Close
// has begun, e is dropped.
func (s *Store) Record(ctx context.Context, e Entry) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		// Logged once: only requests that outlived shutdown get here.
		if s.droppedClosed.Add(1) == 1 {
			logger.Error("Audit log is closed; entries dropped", logger.String("action", e.Action))
		}
		return
	}
	select {
	case s.queue <- complete(ctx, e):
	default:
		// Logged on the first drop and every 1000th after it, not each time.
		if n := s.dropped.Add(1); n%1000 == 1 {
			logger.Error("Audit log queue is full; entries dropped",
				logger.String("action", e.Action), logger.Int64("dropped", n))
		}
	}
}

// run writes queued entries until Close, then writes what is left.
func (s *Store) run() {
	defer close(s.stopped)

	batch := make([]Entry, 0, batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		if _, err := s.coll.InsertMany(ctx, batch, options.InsertMany().SetOrdered(false)); err != nil {
			logger.Error("Audit log entries not written", logger.Int("count", len(batch)), logger.Err(err))
		}
		batch = batch[:0]
	}
	add := func(e Entry) {
		batch = append(batch, e)
		if len(batch) == batchSize {
			flush()
		}
	}

	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()
	for {
		select {
		case e := <-s.queue:
			add(e)
		case <-ticker.C:
			flush()
		case <-s.closing:
			for {
				select {
				case e := <-s.queue:
					add(e)
				default:
					flush()
					return
				}
			}
		}
	}
}

// Close writes the queued entries and disconnects, or gives up when ctx
// ends. Entries recorded from then on are dropped. Calling it again waits
// for the same flush and disconnects at most once.
func (s *Store) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.closing)
	})
	select {
	case <-s.stopped:
	case <-ctx.Done():
		return fmt.Errorf("audit: entries still queued: %w", ctx.Err())
	}
	s.disconnectOnce.Do(func() { s.disconnectErr = s.client.Disconnect(ctx) })
	return s.disconnectErr
}

// List returns one page of entries matching f, newest first, and how many
// match in all.
func (s *Store) List(ctx context.Context, f Filter, page, limit int) ([]Entry, int64, error) {
	filter := bson.D{}
	for _, field := range []struct{ key, value string }{
		{"actor_id", f.ActorID},
		{"target_type", f.TargetType},
		{"target_id", f.TargetID},
		{"action", f.Action},
	} {
		if field.value != "" {
			filter = append(filter, bson.E{Key: field.key, Value: field.value})
		}
	}
	if f.From != nil || f.To != nil {
		at := bson.D{}
		if f.From != nil {
			at = append(at, bson.E{Key: "$gte", Value: *f.From})
		}
		if f.To != nil {
			at = append(at, bson.E{Key: "$lt", Value: *f.To})
		}
		filter = append(filter, bson.E{Key: "at", Value: at})
	}

	total, err := s.coll.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("audit: count entries: %w", err)
	}
	entries, err := s.find(ctx, filter, int64((page-1)*limit), int64(limit))
	if err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

// ListByActor returns at most limit entries of what userID did, newest
// first.
func (s *Store) ListByActor(ctx context.Context, userID string, limit int) ([]Entry, error) {
	return s.find(ctx, bson.D{{Key: "actor_id", Value: userID}}, 0, int64(limit))
}

func (s *Store) find(ctx context.Context, filter bson.D, skip, limit int64) ([]Entry, error) {
	cursor, err := s.coll.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "at", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(skip).
		SetLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("audit: find entries: %w", err)
	}

	entries := []Entry{}
	if err := cursor.All(ctx, &entries); err != nil {
		return nil, fmt.Errorf("audit: read entries: %w", err)
	}
	return entries, nil
}
