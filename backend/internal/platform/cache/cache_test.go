package cache

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// No live Redis is required for these — same reasoning as
// internal/platform/kafka and internal/platform/rabbitmq's tests:
// config-wiring and degrade-gracefully behavior only. Live behavior
// (Publish actually reaching a subscriber, Get/Set/Del/Incr round trips)
// is proven by each phase's live verification against a real Redis
// instance, not by go test.

func TestNewFromEnv_NoURLDisablesEverything(t *testing.T) {
	c := NewFromEnv(func(string) string { return "" }, zap.NewNop())
	if c.Enabled() {
		t.Fatalf("expected a Client with no REDIS_URL to be disabled")
	}

	// Every method must be a safe no-op/miss/unavailable, not a panic, on
	// a disabled Client — this is what every call site in this codebase
	// (skills-service/jobs-service caching, gateway rate limiting, and
	// Phase 3.5's realtime bridge/subscription) relies on to keep working
	// without Redis configured.
	ctx := context.Background()
	if _, ok := c.Get(ctx, "k"); ok {
		t.Errorf("expected Get to report a miss when disabled")
	}
	c.Set(ctx, "k", "v", 0)
	c.Del(ctx, "k")
	if _, ok := c.Incr(ctx, "k", 0); ok {
		t.Errorf("expected Incr to report unavailable when disabled")
	}
	c.Publish(ctx, "chan", "msg")
	if sub, ok := c.Subscribe(ctx, "chan"); ok || sub != nil {
		t.Errorf("expected Subscribe to report unavailable (nil, false) when disabled, got (%v, %v)", sub, ok)
	}
	if err := c.Close(); err != nil {
		t.Errorf("expected Close on a disabled Client to be a safe no-op, got %v", err)
	}
}

func TestNewFromEnv_UnparseableURLDisablesEverything(t *testing.T) {
	c := NewFromEnv(func(key string) string {
		if key == "REDIS_URL" {
			return "not-a-valid-redis-url"
		}
		return ""
	}, zap.NewNop())
	if c.Enabled() {
		t.Fatalf("expected a Client with an unparseable REDIS_URL to be disabled")
	}
}

// PubSub and Subscription are satisfied by *Client at compile time — this
// is what lets internal/gateway/realtime and the onNotification resolver
// depend on the narrow interfaces instead of a concrete *Client, and a
// test double satisfy them too.
var (
	_ PubSub = (*Client)(nil)
)
