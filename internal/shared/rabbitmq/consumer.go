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

// Handler processes a single delivery. Returning nil acks the message; a
// non-nil error nacks it (with requeue, unless it was already redelivered, in
// which case it is dropped to avoid poison-message loops).
type Handler func(ctx context.Context, d amqp.Delivery) error

type registration struct {
	queue      string
	routingKey string
	handler    Handler
}

// Consumer registers handlers per queue and (re)subscribes them, surviving
// connection drops by re-declaring topology and re-consuming whenever the
// channel dies.
type Consumer struct {
	client *Client
	cfg    config.RabbitMQConfig

	mu            sync.Mutex
	registrations []registration

	wg   sync.WaitGroup
	stop chan struct{}
}

// NewConsumer builds a Consumer. Call Register for each queue, then Start.
func NewConsumer(client *Client, cfg config.RabbitMQConfig) *Consumer {
	return &Consumer{
		client: client,
		cfg:    cfg,
		stop:   make(chan struct{}),
	}
}

// Register records a handler for a queue/routing-key. Must be called before
// Start.
func (c *Consumer) Register(queue, routingKey string, h Handler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registrations = append(c.registrations, registration{queue, routingKey, h})
}

// Start subscribes all registrations. Each registration runs in its own
// goroutine that re-subscribes after a reconnect. Non-blocking.
func (c *Consumer) Start(ctx context.Context) error {
	c.mu.Lock()
	regs := make([]registration, len(c.registrations))
	copy(regs, c.registrations)
	c.mu.Unlock()

	for _, reg := range regs {
		c.wg.Add(1)
		go c.runRegistration(ctx, reg)
	}
	return nil
}

// runRegistration keeps a single queue subscribed, re-establishing the channel
// whenever it closes (reconnect, broker restart) until ctx/stop fires.
func (c *Consumer) runRegistration(ctx context.Context, reg registration) {
	defer c.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		default:
		}

		if err := c.subscribeOnce(ctx, reg); err != nil {
			logger.Warn("rabbitmq consumer subscribe failed, retrying",
				logger.String("queue", reg.queue),
				logger.Err(err),
			)
			select {
			case <-ctx.Done():
				return
			case <-c.stop:
				return
			case <-time.After(c.cfg.ReconnectInterval):
			}
		}
	}
}

// subscribeOnce opens a channel, declares topology, sets QoS, and pumps
// deliveries until the channel closes or we're told to stop. Returns nil on a
// clean stop and an error when it should be retried.
func (c *Consumer) subscribeOnce(ctx context.Context, reg registration) error {
	ch, err := c.client.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := DeclareExchange(ch, c.cfg); err != nil {
		return err
	}
	if err := DeclareQueue(ch, c.cfg, reg.queue, reg.routingKey); err != nil {
		return err
	}
	if err := ch.Qos(c.cfg.PrefetchCount, 0, false); err != nil {
		return fmt.Errorf("set qos: %w", err)
	}

	deliveries, err := ch.Consume(
		reg.queue,
		"",    // consumer tag (auto)
		false, // auto-ack OFF — we ack manually
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,
	)
	if err != nil {
		return fmt.Errorf("consume %q: %w", reg.queue, err)
	}

	closeCh := ch.NotifyClose(make(chan *amqp.Error, 1))

	logger.Info("rabbitmq consumer subscribed",
		logger.String("queue", reg.queue),
		logger.String("routing_key", reg.routingKey),
	)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-c.stop:
			return nil
		case amqpErr := <-closeCh:
			if amqpErr != nil {
				return fmt.Errorf("channel closed: %w", amqpErr)
			}
			return fmt.Errorf("channel closed")
		case d, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("delivery channel closed")
			}
			c.handleDelivery(ctx, reg, d)
		}
	}
}

// handleDelivery invokes the handler and acks/nacks accordingly.
func (c *Consumer) handleDelivery(ctx context.Context, reg registration, d amqp.Delivery) {
	if err := reg.handler(ctx, d); err != nil {
		logger.Error("rabbitmq handler error",
			logger.String("queue", reg.queue),
			logger.Bool("redelivered", d.Redelivered),
			logger.Err(err),
		)
		// Requeue once; drop on second failure to avoid poison loops.
		_ = d.Nack(false, !d.Redelivered)
		return
	}
	if err := d.Ack(false); err != nil {
		logger.Warn("rabbitmq ack failed",
			logger.String("queue", reg.queue),
			logger.Err(err),
		)
	}
}

// Stop signals all registration goroutines to exit and waits for them to drain.
func (c *Consumer) Stop() {
	c.mu.Lock()
	select {
	case <-c.stop:
		// already stopped
	default:
		close(c.stop)
	}
	c.mu.Unlock()
	c.wg.Wait()
}
