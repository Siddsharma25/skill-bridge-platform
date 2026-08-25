// Package shutdown provides one reusable graceful-shutdown sequence for
// every cmd/<service>/main.go. Without this, a local Ctrl-C (or a k8s
// SIGTERM during a rollout) drops in-flight requests and — once Kafka
// consumers exist in a later phase — leaves a zombie consumer-group member
// that stalls the next start for ~45s during rebalance. Small amount of
// code, large day-to-day payoff, which is why it's built now instead of
// retrofitted later.
package shutdown

import (
	"context"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// CleanupFunc is one shutdown step (closing a DB pool, stopping a consumer,
// etc). Cleanups run in the order given, sequentially — not in parallel —
// so a later step can assume an earlier one already finished (e.g. "stop
// accepting work" before "close the DB it was using").
type CleanupFunc func(ctx context.Context) error

// Options bundles everything a service might want shut down gracefully.
// Every field is optional (nil/empty is fine) so a caller with, say, no
// HTTP server at all doesn't need to fake one.
type Options struct {
	Logger *zap.Logger
	// GRPCServer is the single-gRPC-server case every cmd/<service>/main.go
	// uses. GRPCServers (below) is the multi-server case cmd/allinone/main.go
	// needs — one process registering all four backend services' gRPC
	// servers at once (see docs/DECISIONS.md's Phase 7 notes). Both may be
	// set at once; every non-nil server across the two is stopped, so this
	// stays a purely additive change for every existing single-server
	// caller.
	GRPCServer  *grpc.Server
	GRPCServers []*grpc.Server
	HTTPServers []*http.Server
	Cleanups    []CleanupFunc
	// Timeout bounds the whole shutdown sequence (HTTP server Shutdown +
	// cleanups); GracefulStop for gRPC has no built-in timeout, so this is
	// also what protects against a gRPC client holding a stream open
	// forever. Defaults to 10s.
	Timeout time.Duration
}

// Wait blocks until SIGINT/SIGTERM, then runs the shutdown sequence:
// stop accepting new gRPC calls and let in-flight ones drain
// (grpcServer.GracefulStop), shut down HTTP servers, then run cleanups in
// order. Intended usage in main():
//
//	shutdown.Wait(ctx, opts)
//	log.Info("shutdown complete")
func Wait(ctx context.Context, opts Options) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	notifyCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-notifyCtx.Done()

	if opts.Logger != nil {
		opts.Logger.Info("shutdown signal received, starting graceful shutdown")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	grpcServers := opts.GRPCServers
	if opts.GRPCServer != nil {
		grpcServers = append([]*grpc.Server{opts.GRPCServer}, grpcServers...)
	}
	if len(grpcServers) > 0 {
		// Every server's GracefulStop runs concurrently, not sequentially —
		// cmd/allinone/main.go registers four of these (auth/skills/users/
		// jobs, each on its own loopback port in one process), and stopping
		// them one at a time would let a slow one alone eat the whole
		// shutdownCtx budget before the next even started draining.
		var wg sync.WaitGroup
		for _, srv := range grpcServers {
			if srv == nil {
				continue
			}
			wg.Add(1)
			go func(s *grpc.Server) {
				defer wg.Done()
				s.GracefulStop()
			}(srv)
		}
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-shutdownCtx.Done():
			if opts.Logger != nil {
				opts.Logger.Warn("grpc graceful stop timed out, forcing stop")
			}
			for _, srv := range grpcServers {
				if srv != nil {
					srv.Stop()
				}
			}
		}
	}

	for _, srv := range opts.HTTPServers {
		if srv == nil {
			continue
		}
		if err := srv.Shutdown(shutdownCtx); err != nil && opts.Logger != nil {
			opts.Logger.Warn("http server shutdown error", zap.Error(err), zap.String("addr", srv.Addr))
		}
	}

	for _, cleanup := range opts.Cleanups {
		if cleanup == nil {
			continue
		}
		if err := cleanup(shutdownCtx); err != nil && opts.Logger != nil {
			opts.Logger.Warn("cleanup error", zap.Error(err))
		}
	}

	if opts.Logger != nil {
		opts.Logger.Info("shutdown complete")
	}
}
