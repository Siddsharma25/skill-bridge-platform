package telemetry

import (
	"context"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func newTestExtension(t *testing.T) (*Metrics, graphql.ResponseInterceptor) {
	t.Helper()
	metrics := NewMetrics(prometheus.NewRegistry())
	ext := NewGraphQLExtension(metrics)
	interceptor, ok := ext.(graphql.ResponseInterceptor)
	if !ok {
		t.Fatal("NewGraphQLExtension's result must implement graphql.ResponseInterceptor")
	}
	return metrics, interceptor
}

func withOperation(name string, op ast.Operation) context.Context {
	return graphql.WithOperationContext(context.Background(), &graphql.OperationContext{
		OperationName: name,
		Operation:     &ast.OperationDefinition{Operation: op},
	})
}

func TestInterceptResponse_RecordsSuccessfulQuery(t *testing.T) {
	metrics, interceptor := newTestExtension(t)

	ctx := withOperation("MyQuery", ast.Query)
	resp := interceptor.InterceptResponse(ctx, func(context.Context) *graphql.Response {
		return &graphql.Response{}
	})
	if resp == nil {
		t.Fatal("expected InterceptResponse to return the wrapped response, got nil")
	}

	got := testutil.ToFloat64(metrics.requestsTotal.WithLabelValues("MyQuery", "query", "ok"))
	if got != 1 {
		t.Errorf("graphql_requests_total{operation=MyQuery,type=query,status=ok} = %v, want 1", got)
	}
}

func TestInterceptResponse_RecordsErrorStatusOnResponseErrors(t *testing.T) {
	metrics, interceptor := newTestExtension(t)

	ctx := withOperation("CreateJob", ast.Mutation)
	interceptor.InterceptResponse(ctx, func(context.Context) *graphql.Response {
		return &graphql.Response{Errors: gqlerror.List{{Message: "boom"}}}
	})

	got := testutil.ToFloat64(metrics.requestsTotal.WithLabelValues("CreateJob", "mutation", "error"))
	if got != 1 {
		t.Errorf("graphql_requests_total{operation=CreateJob,type=mutation,status=error} = %v, want 1", got)
	}
}

func TestInterceptResponse_AnonymousOperationNameFallsBackToLiteral(t *testing.T) {
	metrics, interceptor := newTestExtension(t)

	ctx := withOperation("", ast.Query)
	interceptor.InterceptResponse(ctx, func(context.Context) *graphql.Response {
		return &graphql.Response{}
	})

	got := testutil.ToFloat64(metrics.requestsTotal.WithLabelValues("anonymous", "query", "ok"))
	if got != 1 {
		t.Errorf("expected an empty OperationName to be recorded under \"anonymous\", got %v", got)
	}
}

func TestWebsocketConnections_IncDecTracksActiveCount(t *testing.T) {
	metrics := NewMetrics(prometheus.NewRegistry())

	metrics.IncWebsocketConnections()
	metrics.IncWebsocketConnections()
	metrics.DecWebsocketConnections()

	if got := testutil.ToFloat64(metrics.wsConnections); got != 1 {
		t.Errorf("websocket_connections_active = %v, want 1 after two Inc and one Dec", got)
	}
}
