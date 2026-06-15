// Package rabbitmq owns the shared AMQP client used by the rest of the app.
//
// It lives under internal/shared/ (not pkg/) because it imports
// internal/config, mirroring internal/shared/redis. The client is built with
// the same New(ctx, cfg) (*X, error) + connectivity-check contract as
// redis.New, so the caller decides whether RabbitMQ is load-bearing (fatal) or
// degraded.
package rabbitmq

import (
	"context"
	"fmt"
	"sync"
	"time"

	"venturo-skeleton-go/internal/config"
	"venturo-skeleton-go/pkg/logger"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Client wraps an *amqp.Connection with an auto-reconnect loop. Access to the
// underlying connection is guarded by a RWMutex so Channel() and the reconnect
// goroutine don't race.
type Client struct {
	cfg config.RabbitMQConfig

	mu   sync.RWMutex
	conn *amqp.Connection

	closeOnce sync.Once
	closed    chan struct{}
}

// New dials RabbitMQ and verifies connectivity by opening a probe channel.
// Returns an error so the caller can decide fatal vs degraded (we treat it as
// load-bearing in main.go, consistent with Redis).
func New(ctx context.Context, cfg config.RabbitMQConfig) (*Client, error) {
	c := &Client{
		cfg:    cfg,
		closed: make(chan struct{}),
	}

	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	c.conn = conn

	// Probe: open and immediately close a channel to confirm the connection
	// is usable, not just dialed.
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("rabbitmq probe channel failed: %w", err)
	}
	_ = ch.Close()

	logger.Info("RabbitMQ connected",
		logger.String("host", cfg.Host),
		logger.String("port", cfg.Port),
		logger.String("vhost", cfg.VHost),
		logger.String("exchange", cfg.Exchange),
	)

	go c.watchReconnect(ctx)

	return c, nil
}

// dial opens a single AMQP connection using the configured URL.
func (c *Client) dial(ctx context.Context) (*amqp.Connection, error) {
	dialer := amqp.Config{
		Heartbeat: 10 * time.Second,
		Locale:    "en_US",
	}

	connCh := make(chan *amqp.Connection, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, err := amqp.DialConfig(c.cfg.GetURL(), dialer)
		if err != nil {
			errCh <- err
			return
		}
		connCh <- conn
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errCh:
		return nil, fmt.Errorf("rabbitmq dial failed (%s:%s vhost=%s): %w", c.cfg.Host, c.cfg.Port, c.cfg.VHost, err)
	case conn := <-connCh:
		return conn, nil
	}
}

// watchReconnect listens for connection-close events and re-dials with a fixed
// backoff until the context is cancelled or Close() is called. Consumers
// re-subscribe by watching the same close signal (see consumer.go).
func (c *Client) watchReconnect(ctx context.Context) {
	for {
		c.mu.RLock()
		conn := c.conn
		c.mu.RUnlock()

		if conn == nil {
			return
		}

		closeErr := <-conn.NotifyClose(make(chan *amqp.Error, 1))

		// Graceful close: NotifyClose fires with nil when we Close() ourselves.
		select {
		case <-c.closed:
			return
		case <-ctx.Done():
			return
		default:
		}

		if closeErr != nil {
			logger.Warn("RabbitMQ connection lost, reconnecting",
				logger.Int("code", closeErr.Code),
				logger.String("reason", closeErr.Reason),
			)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-c.closed:
				return
			case <-time.After(c.cfg.ReconnectInterval):
			}

			newConn, err := c.dial(ctx)
			if err != nil {
				logger.Warn("RabbitMQ reconnect attempt failed", logger.Err(err))
				continue
			}

			c.mu.Lock()
			c.conn = newConn
			c.mu.Unlock()

			logger.Info("RabbitMQ reconnected")
			break
		}
	}
}

// Channel opens a new channel on the current connection. Callers that hold a
// channel long-term should re-acquire it on error (channels die with their
// connection).
func (c *Client) Channel() (*amqp.Channel, error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil || conn.IsClosed() {
		return nil, fmt.Errorf("rabbitmq: no active connection")
	}
	return conn.Channel()
}

// NotifyReconnect returns a channel that is closed whenever the underlying
// connection is closed, so long-lived consumers know to re-subscribe. Each
// call returns a fresh notification bound to the current connection.
func (c *Client) NotifyReconnect() <-chan *amqp.Error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		ch := make(chan *amqp.Error, 1)
		close(ch)
		return ch
	}
	return conn.NotifyClose(make(chan *amqp.Error, 1))
}

// Close shuts the connection down. Safe to call multiple times.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closed)
		c.mu.RLock()
		conn := c.conn
		c.mu.RUnlock()
		if conn != nil && !conn.IsClosed() {
			err = conn.Close()
		}
	})
	return err
}
