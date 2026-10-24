package router

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

// fakeStore is a store whose ping and pool state the test sets.
func fakeStore(ping func(ctx context.Context) error, busy func() bool, pings *atomic.Int32) store {
	return store{
		name: "fake",
		ping: func(ctx context.Context) error {
			pings.Add(1)
			return ping(ctx)
		},
		busy: busy,
	}
}

func waitForDeadline(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestStoreCheck(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}

	tests := []struct {
		name      string
		ping      func(ctx context.Context) error
		busy      []bool // busy() answers, in order; the last one repeats
		wantErr   bool
		wantPings int32
	}{
		{name: "reachable", ping: func(context.Context) error { return nil }, busy: []bool{false}, wantPings: 1},
		{name: "refused", ping: func(context.Context) error { return refused }, busy: []bool{false}, wantErr: true, wantPings: 1},
		// Every connection serving requests: busy, not down — and no wait
		{name: "pool saturated", ping: waitForDeadline, busy: []bool{true}, wantPings: 0},
		{name: "pool filled while waiting", ping: waitForDeadline, busy: []bool{false, true}, wantPings: 1},
		{name: "timed out with connections free", ping: waitForDeadline, busy: []bool{false}, wantErr: true, wantPings: 1},
		// A refused connection is an outage even when the pool is full
		// by the time the check looks again.
		{name: "refused, then saturated", ping: func(context.Context) error { return refused }, busy: []bool{false, true}, wantErr: true, wantPings: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			busy := func() bool {
				b := tt.busy[min(calls, len(tt.busy)-1)]
				calls++
				return b
			}
			var pings atomic.Int32
			s := fakeStore(tt.ping, busy, &pings)

			// A short parent deadline keeps the timeout cases quick.
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			err := s.check(ctx)

			if (err != nil) != tt.wantErr {
				t.Errorf("check() = %v, want error: %v", err, tt.wantErr)
			}
			if got := pings.Load(); got != tt.wantPings {
				t.Errorf("pinged %d times, want %d", got, tt.wantPings)
			}
		})
	}
}

func serveReady(r *Readiness) int {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/ready", r.handle)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ready", nil))
	return w.Code
}

func TestReadiness_Drain(t *testing.T) {
	var pings atomic.Int32
	up := fakeStore(func(context.Context) error { return nil }, func() bool { return false }, &pings)
	r := newReadiness(up, up)

	if code := serveReady(r); code != http.StatusOK {
		t.Fatalf("GET /ready = %d, want 200", code)
	}
	r.Drain()
	if code := serveReady(r); code != http.StatusServiceUnavailable {
		t.Errorf("GET /ready while draining = %d, want 503", code)
	}
}

func TestReadiness_OneStoreDown(t *testing.T) {
	var pings atomic.Int32
	up := fakeStore(func(context.Context) error { return nil }, func() bool { return false }, &pings)
	down := fakeStore(func(context.Context) error { return errors.New("auth failed") }, func() bool { return false }, &pings)

	if code := serveReady(newReadiness(up, down)); code != http.StatusServiceUnavailable {
		t.Errorf("GET /ready with a store down = %d, want 503", code)
	}
}

// fakePostgres is a PostgreSQL server that knows just enough protocol for
// pgx to connect and ping.
func fakePostgres(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				be := pgproto3.NewBackend(conn, conn)
				if _, err := be.ReceiveStartupMessage(); err != nil {
					return
				}
				be.Send(&pgproto3.AuthenticationOk{})
				be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
				if be.Flush() != nil {
					return
				}
				for {
					msg, err := be.Receive()
					if err != nil {
						return
					}
					switch msg.(type) {
					case *pgproto3.Query:
						be.Send(&pgproto3.EmptyQueryResponse{})
						be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
						if be.Flush() != nil {
							return
						}
					case *pgproto3.Terminate:
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func newTestPool(t *testing.T, addr string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://app@" + addr + "/timebox?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestPostgresStore(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t, fakePostgres(t))
	s := postgresStore(pool)

	if err := s.check(ctx); err != nil {
		t.Fatalf("check() on an idle pool = %v, want nil", err)
	}

	// Every connection taken by requests: the check answers at once.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	start := time.Now()
	if err := s.check(ctx); err != nil {
		t.Errorf("check() on a saturated pool = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > readyTimeout/2 {
		t.Errorf("check() on a saturated pool took %s", elapsed)
	}
	conn.Release()
}

func TestPostgresStore_Refused(t *testing.T) {
	// A port nobody listens on: bind one, then free it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	err = postgresStore(newTestPool(t, addr)).check(context.Background())
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Errorf("check() with PostgreSQL refusing connections = %v, want connection refused", err)
	}
}

func TestRedisStore_Refused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	client := goredis.NewClient(&goredis.Options{
		Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		// Fail on the first refusal instead of retrying until the deadline
		MaxRetries: -1, DialerRetries: 1,
	})
	t.Cleanup(func() { _ = client.Close() })

	err = redisStore(client).check(context.Background())
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Errorf("check() with Redis refusing connections = %v, want connection refused", err)
	}
}
