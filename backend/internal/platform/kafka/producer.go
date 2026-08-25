package kafka

import (
	"context"
	"strings"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"
)

// Publisher is the subset of Producer's behavior every publishing call
// site depends on — defined as an interface (same reasoning as
// cache.Cache/cache.RateLimiter) so a test can inject a fake and assert
// "this event was published exactly once, with this key/payload" without
// a real Kafka broker. *Producer implements this.
type Publisher interface {
	Publish(ctx context.Context, topic, key string, value []byte)
}

// Producer wraps a franz-go client (nil when Kafka isn't configured) so
// every call site gets the degrade-gracefully behavior for free, same
// pattern as cache.Client wrapping a possibly-nil *redis.Client. Unlike
// cache.Client, though, a failed publish logs at Error level, not Warn —
// see Publish's doc comment for why that distinction matters here.
type Producer struct {
	cl  *kgo.Client
	log *zap.Logger
}

// NewProducerFromEnv builds a Producer from KAFKA_BROKERS (a
// comma-separated list, e.g. "localhost:9092"). If it's unset or the
// client fails to construct, the returned Producer is still safe to use —
// Publish becomes a logged no-op rather than the process failing to start
// or every call site needing its own nil check. This mirrors
// cache.NewFromEnv's degrade-gracefully-on-missing-config pattern.
func NewProducerFromEnv(getenv func(string) string, log *zap.Logger) *Producer {
	brokersRaw := getenv("KAFKA_BROKERS")
	if brokersRaw == "" {
		log.Warn("KAFKA_BROKERS not set; Kafka event publishing is disabled on this instance")
		return &Producer{log: log}
	}
	brokers := splitBrokers(brokersRaw)

	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		// franz-go does not request auto topic creation on a metadata
		// fetch by default, even though the local broker is configured
		// with auto.create.topics.enable=true (see
		// docker/docker-compose.infra.yml): without this, the first
		// publish to any brand-new topic in this codebase fails with
		// UNKNOWN_TOPIC_OR_PARTITION (discovered during this phase's live
		// verification) instead of implicitly creating it. Fine for a
		// single-broker learning setup; a real production cluster would
		// pre-declare topics instead of relying on this.
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		log.Error("failed to create kafka producer client; Kafka event publishing is disabled",
			zap.Strings("brokers", brokers), zap.Error(err))
		return &Producer{log: log}
	}

	log.Info("kafka producer configured", zap.Strings("brokers", brokers))
	return &Producer{cl: cl, log: log}
}

// Enabled reports whether this Producer has a live Kafka client. Exposed
// (like cache.Client.Enabled) purely so a caller that wants to skip
// marshaling a payload entirely can short-circuit early.
func (p *Producer) Enabled() bool {
	return p != nil && p.cl != nil
}

// Publish publishes value under key to topic, synchronously, waiting for
// the broker's ack before returning. On failure — or when Kafka isn't
// configured at all — it logs at Error level and returns, deliberately
// never propagating the failure to the caller: every call site in this
// codebase is "the primary write (e.g. the skill was actually added to
// Postgres) already succeeded; this event is best-effort," consistent
// with the documented "no transactional outbox" gap (see
// docs/DECISIONS.md). Error, not Warn, is deliberate here — unlike a
// Redis cache miss (a pure performance optimization failing safely), a
// dropped domain event is a real data-loss event: jobs-service's snapshot
// projection silently stops reflecting this user's true skill list, or a
// newly created job never gets matched against anyone, until the next
// event for the same key happens to publish successfully.
func (p *Producer) Publish(ctx context.Context, topic, key string, value []byte) {
	if !p.Enabled() {
		p.log.Warn("kafka producer not configured; dropping event",
			zap.String("topic", topic), zap.String("key", key))
		return
	}

	record := &kgo.Record{Topic: topic, Key: []byte(key), Value: value}
	result := p.cl.ProduceSync(ctx, record)
	if err := result.FirstErr(); err != nil {
		p.log.Error("failed to publish kafka event; downstream projections may drift until the next successful publish for this key",
			zap.String("topic", topic), zap.String("key", key), zap.Error(err))
	}
}

// Close closes the underlying Kafka client, flushing any buffered
// records first. Safe to call on a disabled Producer.
func (p *Producer) Close() {
	if p != nil && p.cl != nil {
		p.cl.Close()
	}
}

func splitBrokers(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
