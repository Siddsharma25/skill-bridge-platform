// Package telemetry adds api-gateway's Prometheus metrics — the one
// observability pillar this codebase had deliberately deferred (see
// docs/DECISIONS.md's Scope Cuts: "Grafana+Loki/OpenTelemetry stay
// optional stretch, correlation IDs are the non-optional baseline"). It
// stops well short of that stretch goal on purpose: this is metrics
// (counters/histograms of what happened, scraped periodically), not
// distributed tracing (OpenTelemetry spans following one request across
// every service it touches) or log aggregation — a genuinely smaller,
// self-contained piece with no new external dependency beyond a Go
// library and one HTTP endpoint, unlike tracing which would need a
// collector (Jaeger/Tempo) actually running somewhere to be worth
// anything. See this package's doc comment in docs/DECISIONS.md for why
// this scope, not the wider one.
package telemetry

import (
	"context"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds every metric this package exports. Constructed once at
// startup (see NewMetrics) and shared across every request — Prometheus
// client types are safe for concurrent use, same as this codebase's other
// shared-across-requests dependencies (a *gorm.DB, a gRPC client).
type Metrics struct {
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	wsConnections   prometheus.Gauge
}

// NewMetrics registers every metric with reg and returns a *Metrics ready
// to pass to NewGraphQLExtension and the WebSocket connection-count hooks
// in cmd/api-gateway/main.go. reg is normally prometheus.DefaultRegisterer
// (see promhttp.Handler's own default) — passed explicitly rather than
// implicitly reached for, so a test can register into a throwaway
// registry instead of polluting the process-global one across test runs.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)
	return &Metrics{
		requestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "graphql_requests_total",
			Help: "Total GraphQL operations processed, by operation name, operation type, and outcome.",
		}, []string{"operation", "type", "status"}),
		requestDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name: "graphql_request_duration_seconds",
			Help: "GraphQL operation duration in seconds, by operation name and operation type.",
			// Prometheus's default bucket set tops out at 10s, tuned for
			// typical HTTP handlers; this app's operations are all local
			// gRPC calls expected to resolve in low milliseconds, so a
			// tighter set of buckets gives useful resolution instead of
			// every real request landing in the same bottom bucket.
			Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1},
		}, []string{"operation", "type"}),
		wsConnections: factory.NewGauge(prometheus.GaugeOpts{
			Name: "websocket_connections_active",
			Help: "Currently open, successfully authenticated onNotification WebSocket connections.",
		}),
	}
}

// IncWebsocketConnections and DecWebsocketConnections back
// websocket_connections_active — see cmd/api-gateway/main.go's
// wsInitFunc/wsCloseFunc for where these are actually called and why the
// increment only ever happens on a *successful* auth (an unauthenticated
// upgrade attempt was never a "connection" this gauge should count).
func (m *Metrics) IncWebsocketConnections() { m.wsConnections.Inc() }
func (m *Metrics) DecWebsocketConnections() { m.wsConnections.Dec() }

// gqlExtension implements graphql.HandlerExtension + graphql.ResponseInterceptor
// — the standard gqlgen way to wrap every operation (see the existing
// extension.Introspection{}/extension.AutomaticPersistedQuery{} already
// registered via srv.Use in cmd/api-gateway/main.go; this is the same
// mechanism, just a hand-written extension instead of a library one).
type gqlExtension struct {
	metrics *Metrics
}

// NewGraphQLExtension returns a graphql.HandlerExtension that records
// requestsTotal/requestDuration for every operation gqlgen executes —
// pass it to srv.Use(...) alongside this codebase's other extensions.
func NewGraphQLExtension(metrics *Metrics) graphql.HandlerExtension {
	return &gqlExtension{metrics: metrics}
}

func (e *gqlExtension) ExtensionName() string { return "PrometheusMetrics" }

func (e *gqlExtension) Validate(_ graphql.ExecutableSchema) error { return nil }

// InterceptResponse wraps next() (the rest of the extension chain, then
// the actual resolvers) with a timer, then records one observation keyed
// by operation name/type/outcome. For a subscription, gqlgen calls this
// once per emitted message (see graphql.ResponseInterceptor's own doc
// comment) — each push is recorded as its own observation here, which is
// a deliberate simplification for a learning example: it means
// graphql_request_duration_seconds for a subscription measures "time to
// produce this one push," not "how long the subscription has been open,"
// a distinction worth understanding rather than a bug to fix.
func (e *gqlExtension) InterceptResponse(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	start := time.Now()
	resp := next(ctx)
	duration := time.Since(start).Seconds()

	opCtx := graphql.GetOperationContext(ctx)
	operation := opCtx.OperationName
	if operation == "" {
		operation = "anonymous"
	}
	opType := "query"
	if opCtx.Operation != nil {
		opType = string(opCtx.Operation.Operation)
	}
	status := "ok"
	if resp != nil && len(resp.Errors) > 0 {
		status = "error"
	}

	e.metrics.requestsTotal.WithLabelValues(operation, opType, status).Inc()
	e.metrics.requestDuration.WithLabelValues(operation, opType).Observe(duration)
	return resp
}

var _ graphql.HandlerExtension = (*gqlExtension)(nil)
var _ graphql.ResponseInterceptor = (*gqlExtension)(nil)
