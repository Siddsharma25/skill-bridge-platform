package rabbitmq

import (
	"context"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// ErrNotConfigured is returned by NewConsumerFromEnv when RABBITMQ_URL
// isn't set — callers decide whether that's fatal (it never is in this
// codebase; a consumer that can't start just means that flow doesn't run
// on this instance, logged clearly, same degrade-gracefully pattern as
// kafka.ErrNotConfigured and every other optional external dependency
// here).
var ErrNotConfigured = errors.New("rabbitmq: RABBITMQ_URL is not set")

// Handler processes one message body consumed off a queue. Same
// idempotent-consumer discipline as kafka.Handler expects of its callers:
// a returned error is treated as "this message cannot be processed" and
// nacks it without requeue (see Run's doc comment), never blocking the
// queue or retrying forever.
type Handler func(ctx context.Context, body []byte) error

// Consumer wraps an amqp091-go connection/channel dedicated to consuming
// one queue. api-gateway's realtime bridge (internal/gateway/realtime) is
// the first (and, as of Phase 3.5, only) RabbitMQ consumer written in Go
// in this codebase — every other consumer lives in NestJS
// (notification-service) — so this type deliberately mirrors
// internal/platform/kafka.Consumer's shape (NewXFromEnv degrades
// gracefully, Run blocks until ctx is cancelled, Close releases cleanly)
// rather than inventing a third consumer pattern for a future Go
// consumer to have to learn.
type Consumer struct {
	conn  *amqp.Connection
	ch    *amqp.Channel
	queue string
	log   *zap.Logger
}

// NewConsumerFromEnv builds a Consumer for queue from RABBITMQ_URL,
// declaring the full notifications topology first (see DeclareTopology's
// doc comment — every producer/consumer declares it defensively,
// regardless of process start order, so a publish that arrives before
// this consumer's process has started isn't silently dropped by a queue
// that doesn't exist yet). Returns ErrNotConfigured (not treated as fatal
// by any caller in this codebase) when RABBITMQ_URL is unset, and a
// wrapped error if dialing/opening a channel/declaring topology fails —
// both cases mean this instance simply doesn't run this consumer, logged
// clearly, matching cmd/api-gateway/main.go's degrade-gracefully wiring
// for every other optional dependency (DB, Redis, Kafka, RabbitMQ
// producer).
func NewConsumerFromEnv(getenv func(string) string, queue string, log *zap.Logger) (*Consumer, error) {
	url := getenv("RABBITMQ_URL")
	if url == "" {
		return nil, ErrNotConfigured
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: dial: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("rabbitmq: open channel: %w", err)
	}

	if err := DeclareTopology(ch); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("rabbitmq: declare topology: %w", err)
	}

	// Prefetch 1: only hand this consumer one unacknowledged message at a
	// time. Cheap insurance against a burst of realtime notifications
	// piling up faster than the Redis-publish loop can drain them — the
	// same "a slow/busy consumer shouldn't be handed more than it asked
	// for" reasoning any production AMQP consumer wants, even at this
	// project's local-dev scale.
	if err := ch.Qos(1, 0, false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("rabbitmq: set qos: %w", err)
	}

	log.Info("rabbitmq consumer configured", zap.String("queue", queue))
	return &Consumer{conn: conn, ch: ch, queue: queue, log: log}, nil
}

// Run consumes deliveries from this Consumer's queue, dispatching each to
// handler, until ctx is cancelled, at which point it returns nil — same
// "start in its own goroutine, cancel ctx from a shutdown.CleanupFunc,
// then Close" intended usage as kafka.Consumer.Run (see
// cmd/api-gateway/main.go's wiring and internal/platform/shutdown).
//
// A handler error is logged and the delivery is Nack'd without requeue
// (Nack(false, requeue=false)): if the queue has a dead-letter-exchange
// argument (QueueNotificationsEmail does; QueueNotificationsRealtime
// deliberately doesn't — see topology.go), that either dead-letters or
// silently drops the message, but either way it is never redelivered in a
// loop and never blocks the next message behind it. A handler success
// Acks the delivery.
func (c *Consumer) Run(ctx context.Context, handler Handler) error {
	deliveries, err := c.ch.ConsumeWithContext(ctx, c.queue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("rabbitmq: consume %q: %w", c.queue, err)
	}

	for {
		select {
		case <-ctx.Done():
			// Deliberately does NOT call ch.Cancel here to tell the
			// broker to stop sending: that's itself a synchronous AMQP
			// RPC on this same channel, and issuing it from the one
			// goroutine that has just decided to stop reading
			// `deliveries` risks the exact same wedge Close()'s
			// closeTimeout exists to bound (see that constant's doc
			// comment in close.go) — this loop returning simply stops
			// consuming; Close (bounded, called next by the caller) is
			// what actually tears the channel/connection down, and is
			// the one place this codebase relies on to guarantee
			// shutdown progresses regardless.
			return nil
		case d, ok := <-deliveries:
			if !ok {
				// Channel/connection closed out from under us (e.g. broker
				// restart) — same treatment as ctx cancellation: stop
				// cleanly rather than spin on a closed channel. A restart
				// of this process is this codebase's documented recovery
				// path for a dropped broker connection (see
				// docs/DECISIONS.md's "no reconnect loop" Phase 3 gap,
				// which applies here too).
				return nil
			}
			if handleErr := handler(ctx, d.Body); handleErr != nil {
				c.log.Error("rabbitmq consumer handler failed; message will not be redelivered",
					zap.String("queue", c.queue), zap.Error(handleErr))
				if nackErr := d.Nack(false, false); nackErr != nil {
					c.log.Error("failed to nack rabbitmq message", zap.String("queue", c.queue), zap.Error(nackErr))
				}
				continue
			}
			if ackErr := d.Ack(false); ackErr != nil {
				c.log.Error("failed to ack rabbitmq message", zap.String("queue", c.queue), zap.Error(ackErr))
			}
		}
	}
}

// Close closes the underlying channel and connection. Safe to call on a
// nil Consumer. Bounded by closeTimeout — see that constant's doc comment
// for the real hang this codebase hit live and why an unbounded Close
// here can wedge an entire process's shutdown sequence.
func (c *Consumer) Close() {
	if c == nil {
		return
	}
	closeWithTimeout(func() {
		if c.ch != nil {
			_ = c.ch.Close()
		}
		if c.conn != nil {
			_ = c.conn.Close()
		}
	}, c.log, "consumer queue="+c.queue)
}
