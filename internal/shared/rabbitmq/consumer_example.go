package rabbitmq

import (
	"context"

	"venturo-skeleton-go/pkg/logger"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Example queue/routing-key used purely as an end-to-end proof. Publish a
// message to the app exchange with routing key ExampleRoutingKey (e.g. via the
// RabbitMQ management UI) and watch for the "rabbitmq message received" log.
const (
	ExampleQueue      = "skeleton.example"
	ExampleRoutingKey = "example.ping"
)

// RegisterExampleConsumer wires a minimal log-only handler. It touches no
// business logic — it exists to prove the publish→consume path works.
func RegisterExampleConsumer(c *Consumer) {
	c.Register(ExampleQueue, ExampleRoutingKey, func(_ context.Context, d amqp.Delivery) error {
		logger.Info("rabbitmq message received",
			logger.String("queue", ExampleQueue),
			logger.String("routing_key", d.RoutingKey),
			logger.String("body", string(d.Body)),
		)
		return nil
	})
}
