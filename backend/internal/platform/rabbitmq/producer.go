package rabbitmq

import (
	"context"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// Publisher is the subset of Producer's behavior every publishing call site
// depends on — same interface-for-testability reasoning as
// internal/platform/kafka.Publisher: a test can inject a fake and assert
// "this message was published exactly once, to this queue, with this
// body" without a real RabbitMQ broker. *Producer implements this.
//
// Unlike kafka.Publisher, there's no separate partition key parameter —
// RabbitMQ's default-exchange routing key *is* the destination queue name,
// so "queue" already plays that role.
type Publisher interface {
	Publish(ctx context.Context, queue string, body []byte)
}

// Producer wraps an amqp091-go connection/channel (nil when RabbitMQ isn't
// configured), giving every call site the same degrade-gracefully behavior
// kafka.Producer gives Kafka publishers: Publish becomes a logged no-op
// rather than the process failing to start or every call site needing its
// own nil check.
type Producer struct {
	conn *amqp.Connection
	ch   *amqp.Channel
	log  *zap.Logger
}

// NewProducerFromEnv builds a Producer from RABBITMQ_URL (e.g.
// "amqp://guest:guest@localhost:5672/"). If it's unset, or the connection/
// channel/topology setup fails, the returned Producer is still safe to
// use — Publish becomes a logged no-op — matching
// kafka.NewProducerFromEnv's degrade-gracefully-on-missing-config pattern.
//
// This also declares the full notifications topology (DLX + DLQ + main
// queue, see topology.go) before returning, so that even if
// notification-service hasn't started yet, the queue this producer is
// about to publish into already exists — a RabbitMQ publish to the default
// exchange with a routing key that doesn't match any queue is silently
// dropped, not queued, so skipping this declaration would risk losing the
// very first registration's welcome notification in local dev depending on
// container start order. notification-service's own module declares the
// identical topology defensively too (idempotent as long as arguments
// match — see topology.go's doc comment).
func NewProducerFromEnv(getenv func(string) string, log *zap.Logger) *Producer {
	url := getenv("RABBITMQ_URL")
	if url == "" {
		log.Warn("RABBITMQ_URL not set; RabbitMQ publishing is disabled on this instance")
		return &Producer{log: log}
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		log.Error("failed to connect to rabbitmq; RabbitMQ publishing is disabled", zap.Error(err))
		return &Producer{log: log}
	}

	ch, err := conn.Channel()
	if err != nil {
		log.Error("failed to open rabbitmq channel; RabbitMQ publishing is disabled", zap.Error(err))
		_ = conn.Close()
		return &Producer{log: log}
	}

	if err := DeclareTopology(ch); err != nil {
		log.Error("failed to declare rabbitmq topology; RabbitMQ publishing is disabled", zap.Error(err))
		_ = ch.Close()
		_ = conn.Close()
		return &Producer{log: log}
	}

	log.Info("rabbitmq producer configured", zap.String("queue", QueueNotificationsEmail))
	return &Producer{conn: conn, ch: ch, log: log}
}

// Enabled reports whether this Producer has a live RabbitMQ channel.
// Exposed (like kafka.Producer.Enabled) so a caller that wants to skip
// marshaling a payload entirely can short-circuit early.
func (p *Producer) Enabled() bool {
	return p != nil && p.ch != nil
}

// Publish publishes body to queue via the default exchange (routing key =
// queue name), persistent delivery mode. On failure — or when RabbitMQ
// isn't configured at all — it logs at Error level and returns,
// deliberately never propagating the failure to the caller: every call
// site in this codebase is "the primary write already succeeded (e.g. the
// account was created in Postgres); this notification is best-effort,"
// same reasoning and same log level as kafka.Producer.Publish.
func (p *Producer) Publish(ctx context.Context, queue string, body []byte) {
	if !p.Enabled() {
		p.log.Warn("rabbitmq producer not configured; dropping message", zap.String("queue", queue))
		return
	}

	err := p.ch.PublishWithContext(ctx,
		"",    // default exchange
		queue, // routing key = queue name
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	)
	if err != nil {
		p.log.Error("failed to publish rabbitmq message; downstream notification will not be sent until the next successful publish",
			zap.String("queue", queue), zap.Error(err))
	}
}

// Close closes the underlying channel and connection. Safe to call on a
// disabled Producer. Bounded by closeTimeout — see that constant's doc
// comment (in consumer.go) for the real hang this codebase hit live on
// the consumer side of this same package, and why an unbounded Close on
// a RabbitMQ channel/connection can wedge an entire process's shutdown
// sequence rather than just this one Close call. A pure producer channel
// (no active Consume) is less likely to hit that exact wedge, but the fix
// costs nothing here and keeps both Close paths in this package equally
// safe rather than one of them being an accident away from the same bug.
func (p *Producer) Close() {
	if p == nil {
		return
	}
	closeWithTimeout(func() {
		if p.ch != nil {
			_ = p.ch.Close()
		}
		if p.conn != nil {
			_ = p.conn.Close()
		}
	}, p.log, "producer")
}
