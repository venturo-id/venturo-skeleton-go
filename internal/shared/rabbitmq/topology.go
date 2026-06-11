package rabbitmq

import (
	"fmt"

	"venturo-skeleton-go/internal/config"

	amqp "github.com/rabbitmq/amqp091-go"
)

// DeclareExchange declares the app's topic exchange. Idempotent: declaring an
// already-existing exchange with matching settings is a no-op.
func DeclareExchange(ch *amqp.Channel, cfg config.RabbitMQConfig) error {
	if err := ch.ExchangeDeclare(
		cfg.Exchange, // name
		"topic",      // kind
		true,         // durable
		false,        // auto-delete
		false,        // internal
		false,        // no-wait
		nil,          // args
	); err != nil {
		return fmt.Errorf("declare exchange %q: %w", cfg.Exchange, err)
	}
	return nil
}

// DeclareQueue declares a durable queue and binds it to the app exchange with
// the given routing key. Idempotent.
func DeclareQueue(ch *amqp.Channel, cfg config.RabbitMQConfig, queue, routingKey string) error {
	if _, err := ch.QueueDeclare(
		queue, // name
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,   // args
	); err != nil {
		return fmt.Errorf("declare queue %q: %w", queue, err)
	}

	if err := ch.QueueBind(
		queue,        // queue name
		routingKey,   // routing key
		cfg.Exchange, // exchange
		false,        // no-wait
		nil,          // args
	); err != nil {
		return fmt.Errorf("bind queue %q -> %q (key %q): %w", queue, cfg.Exchange, routingKey, err)
	}
	return nil
}
