package rabbitmq

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

// No live broker is required for these — same reasoning as
// internal/platform/kafka's tests: config-wiring and degrade-gracefully
// behavior only. Live broker behavior (topology declaration actually
// reaching RabbitMQ, Publish reaching a queue, the DLQ round trip) is
// proven by this phase's live verification against
// docker/docker-compose.infra.yml's RabbitMQ service, not by go test.

func TestNewProducerFromEnv_NoURLDisablesPublishing(t *testing.T) {
	p := NewProducerFromEnv(func(string) string { return "" }, zap.NewNop())
	if p.Enabled() {
		t.Fatalf("expected a Producer with no RABBITMQ_URL to be disabled")
	}
	// Publish must be a safe no-op, not a panic, when disabled.
	p.Publish(context.Background(), QueueNotificationsEmail, []byte(`{}`))
	p.Close()
}

func TestNewProducerFromEnv_UnreachableBrokerDisablesPublishing(t *testing.T) {
	// Deliberately pointed at a port nothing is listening on locally, so
	// this proves the "connect fails -> degrade gracefully" path without
	// requiring a live broker in CI.
	p := NewProducerFromEnv(func(key string) string {
		if key == "RABBITMQ_URL" {
			return "amqp://guest:guest@127.0.0.1:1/"
		}
		return ""
	}, zap.NewNop())
	if p.Enabled() {
		t.Fatalf("expected a Producer with an unreachable RABBITMQ_URL to be disabled")
	}
	p.Publish(context.Background(), QueueNotificationsEmail, []byte(`{}`))
	p.Close()
}

func TestQueueAndExchangeNames(t *testing.T) {
	// Pinned as an explicit test (not just a doc comment) because the
	// NestJS side (backend/notification-service/src/rabbitmq/rabbitmq.module.ts)
	// has to match these string-for-string — a silent rename here would
	// otherwise only surface as "messages published but never consumed" at
	// runtime.
	if QueueNotificationsEmail != "notifications.email" {
		t.Errorf("QueueNotificationsEmail = %q, want %q", QueueNotificationsEmail, "notifications.email")
	}
	if ExchangeNotificationsDLX != "notifications.dlx" {
		t.Errorf("ExchangeNotificationsDLX = %q, want %q", ExchangeNotificationsDLX, "notifications.dlx")
	}
	if QueueNotificationsEmailDLQ != "notifications.email.dlq" {
		t.Errorf("QueueNotificationsEmailDLQ = %q, want %q", QueueNotificationsEmailDLQ, "notifications.email.dlq")
	}
	if QueueNotificationsRealtime != "notifications.realtime" {
		t.Errorf("QueueNotificationsRealtime = %q, want %q", QueueNotificationsRealtime, "notifications.realtime")
	}
}

// Phase 3.5's rabbitmq.Consumer (api-gateway's realtime bridge) follows
// the same degrade-gracefully-on-missing-config contract as Producer
// above — no live broker required for these two cases.

func TestNewConsumerFromEnv_NoURLReturnsErrNotConfigured(t *testing.T) {
	_, err := NewConsumerFromEnv(func(string) string { return "" }, QueueNotificationsRealtime, zap.NewNop())
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestNewConsumerFromEnv_UnreachableBrokerReturnsError(t *testing.T) {
	_, err := NewConsumerFromEnv(func(key string) string {
		if key == "RABBITMQ_URL" {
			return "amqp://guest:guest@127.0.0.1:1/"
		}
		return ""
	}, QueueNotificationsRealtime, zap.NewNop())
	if err == nil {
		t.Fatal("expected an error connecting to an unreachable broker")
	}
	if errors.Is(err, ErrNotConfigured) {
		t.Fatalf("an unreachable broker should not be reported as ErrNotConfigured (that's specifically for a missing RABBITMQ_URL): %v", err)
	}
}

// Close on a nil Consumer must be a safe no-op, mirroring Producer.Close
// and every other Close in this codebase's degrade-gracefully pattern —
// cmd/api-gateway/main.go's shutdown.CleanupFunc calls this unconditionally
// even when RabbitMQ was never configured.
func TestConsumerClose_NilIsSafe(_ *testing.T) {
	var c *Consumer
	c.Close()
}

// TestCloseWithTimeout_BoundsAHangingClose proves closeTimeout's whole
// reason for existing (see close.go's doc comment): a live, reproduced
// hang during Phase 3.5's graceful-shutdown verification showed
// amqp091-go's Channel.Close blocking forever in some circumstances,
// wedging this codebase's entire shutdown.Wait sequence. closeWithTimeout
// must return well before a hanging fn ever would, not just "eventually."
func TestCloseWithTimeout_BoundsAHangingClose(t *testing.T) {
	blockForever := make(chan struct{}) // never closed
	start := time.Now()
	closeWithTimeout(func() {
		<-blockForever
	}, zap.NewNop(), "test")
	elapsed := time.Since(start)

	if elapsed >= 10*time.Second {
		t.Fatalf("closeWithTimeout took %s — expected it to give up around closeTimeout (%s), not hang", elapsed, closeTimeout)
	}
	if elapsed < closeTimeout {
		t.Fatalf("closeWithTimeout returned after only %s, before closeTimeout (%s) elapsed — should wait at least that long before giving up", elapsed, closeTimeout)
	}
}

// TestCloseWithTimeout_ReturnsImmediatelyOnFastClose proves the common
// case isn't penalized: a close that finishes quickly shouldn't wait out
// the full timeout.
func TestCloseWithTimeout_ReturnsImmediatelyOnFastClose(t *testing.T) {
	start := time.Now()
	closeWithTimeout(func() {}, zap.NewNop(), "test")
	if elapsed := time.Since(start); elapsed >= closeTimeout {
		t.Fatalf("closeWithTimeout took %s for an instantly-returning fn — expected it not to wait out the timeout unnecessarily", elapsed)
	}
}
