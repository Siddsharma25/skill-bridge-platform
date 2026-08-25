package auth

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/auth/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/jwks"
)

// fakePublisher is a trivial in-memory stand-in for rabbitmq.Publisher —
// same reasoning/shape as users.fakePublisher for kafka.Publisher: lets a
// test assert whether Register published (or, as below, correctly did
// *not* publish) without a real RabbitMQ broker.
type fakePublisher struct {
	calls int
}

func (f *fakePublisher) Publish(_ context.Context, _ string, _ []byte) {
	f.calls++
}

// These tests deliberately never touch a real database — there is no live
// Supabase project yet (see docs/DECISIONS.md) — so they cover the
// request-validation and no-DB-configured code paths, which run before any
// database access is attempted. The full Register->Login round trip was
// verified manually against a throwaway local Postgres container during
// development; see docs/DECISIONS.md for why that isn't wired into `go
// test` (it would make CI depend on a live database, which the plan
// explicitly avoids).

func testServer(t *testing.T, publisher *fakePublisher) *Server {
	t.Helper()
	kp, err := jwks.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate test keypair: %v", err)
	}
	log := zap.NewNop()
	// db is intentionally nil: these tests only exercise validation and
	// the "database not configured" degrade path. googleExchanger is also
	// nil (not exercised here) — see oauth_test.go. publisher may be nil
	// (rabbitPublisher is documented as nil-safe) or a *fakePublisher so a
	// test can assert Register never reaches the publish step when the
	// primary write (Create) never happened.
	if publisher == nil {
		return NewServer(nil, kp, "test-issuer", nil, nil, log)
	}
	return NewServer(nil, kp, "test-issuer", nil, publisher, log)
}

func TestRegister_RejectsMissingFields(t *testing.T) {
	s := testServer(t, nil)

	cases := []*authv1.RegisterRequest{
		{Email: "", Password: "password123"},
		{Email: "a@example.com", Password: ""},
		{Email: "a@example.com", Password: "short"},
	}
	for _, req := range cases {
		_, err := s.Register(context.Background(), req)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("Register(%+v): expected InvalidArgument, got %v", req, err)
		}
	}
}

func TestRegister_NoDBReturnsUnavailable(t *testing.T) {
	s := testServer(t, nil)
	_, err := s.Register(context.Background(), &authv1.RegisterRequest{
		Email:    "a@example.com",
		Password: "password123",
	})
	if status.Code(err) != codes.Unavailable {
		t.Errorf("expected Unavailable when db is nil, got %v", err)
	}
}

// TestRegister_NoDBNeverPublishes proves the best-effort welcome
// notification is only ever attempted after the primary write (the
// account row) actually succeeds — Register must fail fast on
// codes.Unavailable here without touching s.rabbitPublisher at all, or a
// future refactor could start "notifying" about accounts that were never
// created.
func TestRegister_NoDBNeverPublishes(t *testing.T) {
	fp := &fakePublisher{}
	s := testServer(t, fp)
	_, err := s.Register(context.Background(), &authv1.RegisterRequest{
		Email:    "a@example.com",
		Password: "password123",
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable when db is nil, got %v", err)
	}
	if fp.calls != 0 {
		t.Errorf("expected 0 publish calls when Register fails before the DB write, got %d", fp.calls)
	}
}

func TestLogin_RejectsMissingFields(t *testing.T) {
	s := testServer(t, nil)
	_, err := s.Login(context.Background(), &authv1.LoginRequest{Email: "", Password: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestLogin_NoDBReturnsUnavailable(t *testing.T) {
	s := testServer(t, nil)
	_, err := s.Login(context.Background(), &authv1.LoginRequest{
		Email:    "a@example.com",
		Password: "password123",
	})
	if status.Code(err) != codes.Unavailable {
		t.Errorf("expected Unavailable when db is nil, got %v", err)
	}
}
