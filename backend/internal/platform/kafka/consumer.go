package kafka

import (
	"context"
	"errors"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
)

// ErrNotConfigured is returned by NewConsumerFromEnv when KAFKA_BROKERS
// isn't set — callers decide whether that's fatal (it never is in this
// codebase; a consumer that can't start just means that projection/worker
// doesn't run on this instance, logged clearly, same degrade-gracefully
// pattern as a missing DATABASE_URL/REDIS_URL elsewhere).
var ErrNotConfigured = errors.New("kafka: KAFKA_BROKERS is not set")

// Message is one decoded Kafka record, deliberately decoupled from
// franz-go's own *kgo.Record so every internal/<service> package that
// consumes events depends only on this package, not on the concrete
// client library — keeps franz-go contained to internal/platform/kafka,
// same reasoning gorm.DB is contained to internal/platform/db call sites
// rather than every service importing a driver package directly.
type Message struct {
	Topic string
	Key   []byte
	Value []byte
}

// Handler processes one Message. A returned error is logged by Run but
// does not stop the consumer or block committing that record's offset
// (see Run's doc comment) — handlers in this codebase are written to be
// idempotent (upsert, or a full replace keyed by an authoritative payload)
// specifically so that at-least-once redelivery, whether from a handler
// error here or an ordinary consumer restart, never corrupts state. See
// docs/DECISIONS.md's "no transactional outbox" note for the broader
// reasoning this follows.
type Handler func(ctx context.Context, msg Message) error

// Consumer wraps a franz-go client configured as a named consumer group
// subscribed to a fixed set of topics. Every Kafka-consuming goroutine in
// this codebase (jobs-service's snapshot projection, matching worker, and
// cross-service cache invalidator — see internal/jobs) gets its own
// Consumer/consumer-group, so each can be scaled or restarted
// independently and none can block another by processing slowly.
type Consumer struct {
	cl      *kgo.Client
	log     *zap.Logger
	groupID string
	topics  []string
}

// ConsumerConfig configures NewConsumer.
type ConsumerConfig struct {
	Brokers []string
	GroupID string
	Topics  []string
}

// NewConsumerFromEnv reads KAFKA_BROKERS (comma-separated) and builds a
// Consumer for groupID/topics. Returns ErrNotConfigured (not a fatal
// error) when KAFKA_BROKERS is unset, matching this codebase's
// degrade-gracefully convention for every optional external dependency.
func NewConsumerFromEnv(getenv func(string) string, groupID string, topics []string, log *zap.Logger) (*Consumer, error) {
	brokersRaw := getenv("KAFKA_BROKERS")
	if brokersRaw == "" {
		return nil, ErrNotConfigured
	}
	return NewConsumer(ConsumerConfig{
		Brokers: splitBrokers(brokersRaw),
		GroupID: groupID,
		Topics:  topics,
	}, log)
}

// NewConsumer builds a Consumer from an explicit config.
func NewConsumer(cfg ConsumerConfig, log *zap.Logger) (*Consumer, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(cfg.GroupID),
		kgo.ConsumeTopics(cfg.Topics...),
		// New consumer groups (every group here, on a fresh local broker)
		// start from the beginning of each topic rather than only
		// newly-produced records — otherwise a consumer started after its
		// publisher already ran (the common case in local dev / this
		// phase's live verification) would see nothing until the next
		// publish.
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		return nil, err
	}
	return &Consumer{cl: cl, log: log, groupID: cfg.GroupID, topics: cfg.Topics}, nil
}

// Run polls for records and dispatches each to handler, sequentially,
// until ctx is cancelled, at which point it returns nil. Offsets are
// autocommitted by the underlying client on its default interval — this
// codebase already documents "at-least-once delivery + idempotent
// consumers, no transactional outbox" as a deliberate gap (see
// docs/DECISIONS.md), so Run does not attempt per-record synchronous
// commit-after-success bookkeeping; a handler error is logged and the
// loop moves on rather than retrying that record forever (a poison
// message blocking an entire partition indefinitely would be worse than
// the small redelivery window autocommit already accepts).
//
// Intended usage: start Run in its own goroutine per consumer, and cancel
// ctx from an internal/platform/shutdown CleanupFunc, then call Close
// once Run has returned — see cmd/jobs-service/main.go and
// cmd/users-service... wiring, and this comment: skipping this is exactly
// the "zombie consumer-group member, ~45s rebalance stall on next start"
// failure mode internal/platform/shutdown's own doc comment warns about.
func (c *Consumer) Run(ctx context.Context, handler Handler) error {
	for {
		if ctx.Err() != nil {
			return nil
		}

		fetches := c.cl.PollFetches(ctx)
		if ctx.Err() != nil {
			return nil
		}

		fetches.EachError(func(topic string, partition int32, err error) {
			c.log.Error("kafka fetch error",
				zap.String("group_id", c.groupID), zap.String("topic", topic),
				zap.Int32("partition", partition), zap.Error(err))
		})

		fetches.EachRecord(func(r *kgo.Record) {
			msg := Message{Topic: r.Topic, Key: r.Key, Value: r.Value}
			if err := handler(ctx, msg); err != nil {
				c.log.Error("kafka record handler failed",
					zap.String("group_id", c.groupID), zap.String("topic", r.Topic), zap.Error(err))
			}
		})
	}
}

// Close leaves the consumer group and closes the underlying client. This
// is what prevents the zombie-consumer-group-member problem
// internal/platform/shutdown's doc comment describes: franz-go's
// Client.Close sends a LeaveGroup before disconnecting, so the broker
// evicts this member immediately rather than waiting out its session
// timeout before the next rebalance can proceed.
func (c *Consumer) Close() {
	if c != nil && c.cl != nil {
		c.cl.Close()
	}
}
