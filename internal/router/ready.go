package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"timebox-be/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

// readyTimeout bounds each store's check. Keep the readiness probe's
// timeoutSeconds above it, or a slow answer fails the probe before this
// check can call the store busy rather than down.
const readyTimeout = 2 * time.Second

var errDraining = errors.New("draining before shutdown")

// store is one dependency GET /ready checks.
type store struct {
	name string
	ping func(ctx context.Context) error
	// busy reports whether every pooled connection is in use. A check
	// then has to wait for the pool, not for the store, and a pool
	// saturated by traffic must not take every replica out of the
	// service at once: busy counts as ready.
	busy func() bool
}

// check answers nil when the store is reachable or merely busy. A
// refused connection, failed authentication or unknown host still fails.
func (s store) check(ctx context.Context) error {
	if s.busy() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()

	err := s.ping(ctx)
	if err != nil && ctx.Err() != nil && s.busy() {
		return nil // the pool filled up while the check waited for it
	}
	if err != nil {
		return fmt.Errorf("%s: %w", s.name, err)
	}
	return nil
}

func postgresStore(pool *pgxpool.Pool) store {
	return store{
		name: "postgres",
		ping: pool.Ping,
		busy: func() bool {
			stat := pool.Stat()
			return stat.AcquiredConns() >= stat.MaxConns()
		},
	}
}

func redisStore(client *goredis.Client) store {
	return store{
		name: "redis",
		ping: func(ctx context.Context) error { return client.Ping(ctx).Err() },
		busy: func() bool {
			stats := client.PoolStats()
			return int(stats.TotalConns)-int(stats.IdleConns) >= client.Options().PoolSize
		},
	}
}

// Readiness answers GET /ready: 200 while PostgreSQL and Redis answer
// (or are only busy), 503 when one is down or the server is draining.
type Readiness struct {
	stores   []store
	draining atomic.Bool
}

func newReadiness(stores ...store) *Readiness {
	return &Readiness{stores: stores}
}

// Drain makes /ready answer 503 from now on, so the load balancer stops
// sending new requests before the server shuts down. /health is not
// affected.
func (r *Readiness) Drain() {
	r.draining.Store(true)
}

// check runs every store's check at once, each within readyTimeout.
func (r *Readiness) check(ctx context.Context) error {
	if r.draining.Load() {
		return errDraining
	}
	errs := make([]error, len(r.stores))
	var wg sync.WaitGroup
	for i, s := range r.stores {
		wg.Go(func() { errs[i] = s.check(ctx) })
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (r *Readiness) handle(c *gin.Context) {
	if err := r.check(c.Request.Context()); err != nil {
		if !errors.Is(err, errDraining) {
			logger.Warn("Readiness check failed", logger.Err(err))
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
