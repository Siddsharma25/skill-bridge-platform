package tracing

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// No live OTLP collector is required for this — same reasoning as
// internal/platform/cache's tests: config-wiring and degrade-gracefully
// behavior only. Live behavior (a real span actually reaching Jaeger) is
// proven by live verification against a real collector, not go test.

func TestInitTracerProvider_EmptyEndpointDegradesToNoop(t *testing.T) {
	shutdown := InitTracerProvider(t.Context(), "test-service", "", zap.NewNop())
	if shutdown == nil {
		t.Fatal("expected a non-nil shutdown func even when tracing is disabled")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("expected the no-op shutdown to be a safe no-op, got %v", err)
	}
}
