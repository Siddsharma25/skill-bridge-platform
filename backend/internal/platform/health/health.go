// Package health wires up both flavors of health check every service in
// this monorepo needs: the standard grpc.health.v1.Health service (so
// grpc_health_probe/grpcurl and later k8s gRPC probes work uniformly) and
// plain HTTP /healthz + /readyz for anything that only speaks HTTP
// (Render's health check, a browser, curl during local dev).
//
// The liveness/readiness split is deliberate, not redundant: /healthz
// answers "is the process alive at all" (no dependency checks — a slow
// Postgres should never make an orchestrator kill and restart a healthy
// process), while /readyz answers "can this instance actually serve
// traffic right now" (DB reachable, etc.) and is what a load balancer or
// k8s Service should gate routing on.
package health

import (
	"context"
	"encoding/json"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// NewGRPCServer returns a grpc.health.v1.Health implementation and registers
// it against srv. The returned *health.Server also lets the caller flip
// SERVING/NOT_SERVING per-service if finer-grained health ever matters.
func NewGRPCServer(srv *grpc.Server) *health.Server {
	h := health.NewServer()
	healthpb.RegisterHealthServer(srv, h)
	// Overall server status starts SERVING immediately; readiness (DB etc.)
	// is a separate concern surfaced via HTTP /readyz and, if needed later,
	// a named service status here.
	h.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	return h
}

// ReadyCheckFunc reports whether the service can currently serve traffic.
// It returns an error describing what's wrong, or nil when ready.
type ReadyCheckFunc func(ctx context.Context) error

// Mux returns an *http.ServeMux with /healthz and /readyz registered. It's
// deliberately a bare mux (not a full http.Server) so callers can add other
// routes (e.g. auth-service's /.well-known/jwks.json) to the same server
// without health/jwks needing to know about each other.
func Mux(ready ReadyCheckFunc) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if ready == nil {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}
		if err := ready(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status": "not_ready",
				"error":  err.Error(),
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	return mux
}
