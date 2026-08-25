package shutdown

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/health"
)

// TestWait_StopsMultipleGRPCServers is the one test this phase's
// cmd/allinone addition actually needs: proof that GRPCServers (plural —
// new in Phase 7, see docs/DECISIONS.md's notes on the allinone binary
// registering four backend gRPC servers in one process) actually stops
// every listed server, not just the first one a naive single-field
// implementation would touch, and that Wait doesn't hang doing it. Four
// real grpc.Servers stand in for allinone's auth/skills/users/jobs
// servers, each serving the standard health service so "did this one
// actually stop" is a real, checkable client-visible property (a fresh
// dial after shutdown failing) rather than an assumption about internal
// state.
func TestWait_StopsMultipleGRPCServers(t *testing.T) {
	const n = 4
	servers := make([]*grpc.Server, n)
	addrs := make([]string, n)

	for i := 0; i < n; i++ {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		addrs[i] = lis.Addr().String()

		srv := grpc.NewServer()
		health.NewGRPCServer(srv)
		servers[i] = srv
		go func() { _ = srv.Serve(lis) }()
	}

	// Confirm all four are actually up and answering before shutdown —
	// otherwise a later "dial fails" observation after Wait returns would
	// be meaningless (it might never have been reachable at all).
	for i, addr := range addrs {
		if !healthCheckSucceeds(t, addr) {
			t.Fatalf("server %d not serving before shutdown", i)
		}
	}

	// Wait derives its own signal-triggered context from the ctx it's
	// given (see shutdown.go: signal.NotifyContext(ctx, ...)), which fires
	// on either an actual OS signal *or* ctx's own cancellation — so
	// cancelling waitCtx directly here exercises the exact same shutdown
	// path a real SIGTERM would, without this test process having to
	// signal itself (fragile under a test harness/process-group and not
	// needed just to prove GRPCServers works).
	waitCtx, triggerShutdown := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Wait(waitCtx, Options{
			GRPCServers: servers,
			Timeout:     2 * time.Second,
		})
		close(done)
	}()

	triggerShutdown()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not return after shutdown was triggered — a GracefulStop on one server blocking the others?")
	}

	// Every one of the four must have actually stopped — not just the
	// first — which is the entire point of GRPCServers over the older
	// single-GRPCServer field.
	for i, addr := range addrs {
		if healthCheckSucceeds(t, addr) {
			t.Fatalf("server %d is still serving after Wait returned", i)
		}
	}
}

// healthCheckSucceeds dials addr fresh and issues one health Check RPC,
// bounded by a short deadline so a stopped server (connection refused, or
// no response) fails fast instead of hanging the test.
func healthCheckSucceeds(t *testing.T, addr string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()

	client := healthpb.NewHealthClient(conn)
	resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	return err == nil && resp.GetStatus() == healthpb.HealthCheckResponse_SERVING
}
