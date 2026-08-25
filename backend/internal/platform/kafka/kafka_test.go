package kafka

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

// No live broker is required for these — same reasoning as
// internal/platform/db's tests: config-wiring and degrade-gracefully
// behavior only. Live broker behavior (Consumer.Run reading real
// records, Producer.Publish actually reaching a topic) is proven by this
// phase's live verification against docker/docker-compose.infra.yml's
// Kafka service, not by go test.

func TestNewProducerFromEnv_NoBrokersDisablesPublishing(t *testing.T) {
	p := NewProducerFromEnv(func(string) string { return "" }, zap.NewNop())
	if p.Enabled() {
		t.Fatalf("expected a Producer with no KAFKA_BROKERS to be disabled")
	}
	// Publish must be a safe no-op, not a panic, when disabled.
	p.Publish(context.Background(), "some.topic", "key", []byte("value"))
}

func TestNewProducerFromEnv_ConfiguresWhenBrokersSet(t *testing.T) {
	// kgo.NewClient doesn't dial eagerly, so this succeeds even with no
	// broker actually listening — only Publish would fail against a dead
	// broker, which is covered by the live verification, not this test.
	p := NewProducerFromEnv(func(key string) string {
		if key == "KAFKA_BROKERS" {
			return "localhost:9092"
		}
		return ""
	}, zap.NewNop())
	defer p.Close()
	if !p.Enabled() {
		t.Fatalf("expected a Producer with KAFKA_BROKERS set to be enabled")
	}
}

func TestNewConsumerFromEnv_NoBrokersReturnsErrNotConfigured(t *testing.T) {
	_, err := NewConsumerFromEnv(func(string) string { return "" }, "test-group", []string{"some.topic"}, zap.NewNop())
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestNewConsumerFromEnv_ConfiguresWhenBrokersSet(t *testing.T) {
	c, err := NewConsumerFromEnv(func(key string) string {
		if key == "KAFKA_BROKERS" {
			return "localhost:9092,localhost:9093"
		}
		return ""
	}, "test-group", []string{"some.topic"}, zap.NewNop())
	if err != nil {
		t.Fatalf("unexpected error constructing consumer: %v", err)
	}
	defer c.Close()
}

func TestSplitBrokers(t *testing.T) {
	got := splitBrokers(" localhost:9092 , localhost:9093,,localhost:9094 ")
	want := []string{"localhost:9092", "localhost:9093", "localhost:9094"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
