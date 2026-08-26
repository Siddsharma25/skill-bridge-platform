// Package tracing adds distributed tracing — the piece of observability
// this codebase's telemetry work (internal/platform/telemetry) explicitly
// left for later: metrics tell you "how many, how fast, in aggregate";
// tracing tells you "for this *one* request, which service did it visit,
// in what order, and how long did each hop take." The correlation ID
// (internal/platform/requestid) already lets you find every log line for
// one request across services; tracing adds the same idea as a visual
// waterfall of spans instead of a text grep.
//
// Same degrade-gracefully convention as every other optional dependency
// in this codebase: no OTEL_EXPORTER_OTLP_ENDPOINT configured means
// InitTracerProvider returns a no-op shutdown and the process runs with
// OpenTelemetry's default no-op tracer — every Start(ctx, "span-name")
// call anywhere in the codebase becomes a cheap no-op, not an error.
package tracing

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.uber.org/zap"
)

// InitTracerProvider configures OpenTelemetry's global tracer provider to
// export spans (batched, over OTLP/gRPC) to otlpEndpoint — normally a
// local Jaeger or Grafana Tempo instance's OTLP gRPC port (4317). Returns
// a shutdown func that flushes any buffered spans and closes the
// exporter; callers should defer it.
//
// If otlpEndpoint is empty, this is a no-op: the global tracer provider
// is left at OpenTelemetry's own no-op default, logged once here rather
// than the process failing to start or every call site needing its own
// "is tracing configured" check — the same pattern cache.NewFromEnv and
// every other optional dependency in this codebase already follows.
func InitTracerProvider(ctx context.Context, serviceName, otlpEndpoint string, log *zap.Logger) (shutdown func(context.Context) error) {
	noop := func(context.Context) error { return nil }

	if otlpEndpoint == "" {
		log.Warn("OTEL_EXPORTER_OTLP_ENDPOINT not set; distributed tracing is disabled on this instance")
		return noop
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(otlpEndpoint),
		// WithInsecure: this exporter only ever talks to a local Jaeger/
		// Tempo instance over a loopback/private Docker network in this
		// project's setup (see docker/docker-compose.observability.yml) —
		// there is no real TLS-terminating collector to talk to here,
		// same reasoning docker-compose's other internal-only hops use
		// plain connections.
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithTimeout(5*time.Second),
	)
	if err != nil {
		log.Warn("failed to create OTLP trace exporter; distributed tracing is disabled",
			zap.String("endpoint", otlpEndpoint), zap.Error(err))
		return noop
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		// A resource describes *this process* to whatever's collecting
		// spans (its service name, mainly) — failing to build one is
		// vanishingly unlikely (no I/O involved) but degrades the same
		// way every other failure here does rather than risking a
		// startup panic over an observability nicety.
		log.Warn("failed to build OTel resource; distributed tracing is disabled", zap.Error(err))
		return noop
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	// The W3C Trace Context propagator is what actually carries a trace
	// ID across the gRPC hop as metadata — otelgrpc's client/server stats
	// handlers (see cmd/api-gateway/main.go and cmd/skills-service/
	// main.go) read/write it via this propagator, not otelgrpc itself
	// choosing a wire format. Without registering this, every hop would
	// start a brand new, disconnected trace instead of extending the
	// caller's.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	log.Info("distributed tracing configured", zap.String("service_name", serviceName),
		zap.String("otlp_endpoint", otlpEndpoint))

	return tp.Shutdown
}
