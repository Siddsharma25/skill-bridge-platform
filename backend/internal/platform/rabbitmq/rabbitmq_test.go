package rabbitmq

import (
	"context"
	"testing"

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
}
