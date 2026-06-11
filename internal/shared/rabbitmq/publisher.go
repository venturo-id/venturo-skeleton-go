package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"venturo-skeleton-go/internal/config"
	"venturo-skeleton-go/pkg/logger"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Publisher publishes JSON messages to the app exchange with publisher confirms
// and persistent delivery. It lazily (re)acquires its channel so it survives
// reconnects driven by the Client.
type Publisher struct {
	client *Client
	cfg    config.RabbitMQConfig

	mu      sync.Mutex
	channel *amqp.Channel
	confirm chan amqp.Confirmation
}

// NewPublisher builds a Publisher and declares the exchange. The exchange
// declaration is idempotent, so calling this alongside the consumer is safe.
func NewPublisher(client *Client, cfg config.RabbitMQConfig) (*Publisher, error) {
	p := &Publisher{client: client, cfg: cfg}
	if _, err := p.acquire(); err != nil {
		return nil, err
	}
	return p, nil
}

// acquire returns a confirm-mode channel, opening a fresh one if the current
// channel is nil or closed. Caller must hold no assumptions across reconnects.
func (p *Publisher) acquire() (*amqp.Channel, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.channel != nil && !p.channel.IsClosed() {
		return p.channel, nil
	}

	ch, err := p.client.Channel()
	if err != nil {
		return nil, fmt.Errorf("publisher: open channel: %w", err)
	}

	if err := DeclareExchange(ch, p.cfg); err != nil {
		_ = ch.Close()
		return nil, err
	}

	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("publisher: put channel in confirm mode: %w", err)
	}

	p.channel = ch
	p.confirm = ch.NotifyPublish(make(chan amqp.Confirmation, 1))
	return ch, nil
}

// Publish JSON-marshals body and publishes it to the app exchange under
// routingKey, waiting up to PublishTimeout for the broker confirm.
func (p *Publisher) Publish(ctx context.Context, routingKey string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("publisher: marshal body: %w", err)
	}

	ch, err := p.acquire()
	if err != nil {
		return err
	}

	pubCtx, cancel := context.WithTimeout(ctx, p.cfg.PublishTimeout)
	defer cancel()

	if err := ch.PublishWithContext(
		pubCtx,
		p.cfg.Exchange,
		routingKey,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         payload,
		},
	); err != nil {
		// Drop the channel so the next call re-acquires (it may be dead).
		p.mu.Lock()
		p.channel = nil
		p.mu.Unlock()
		return fmt.Errorf("publisher: publish to %q (key %q): %w", p.cfg.Exchange, routingKey, err)
	}

	p.mu.Lock()
	confirm := p.confirm
	p.mu.Unlock()

	select {
	case <-pubCtx.Done():
		return fmt.Errorf("publisher: confirm timeout for key %q: %w", routingKey, pubCtx.Err())
	case c, ok := <-confirm:
		if !ok {
			return fmt.Errorf("publisher: confirm channel closed for key %q", routingKey)
		}
		if !c.Ack {
			return fmt.Errorf("publisher: message nacked by broker for key %q", routingKey)
		}
	}

	logger.Debug("rabbitmq message published",
		logger.String("routing_key", routingKey),
		logger.Int("bytes", len(payload)),
	)
	return nil
}

// Close releases the publisher channel.
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.channel != nil && !p.channel.IsClosed() {
		return p.channel.Close()
	}
	return nil
}
