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

// These tests deliberately never touch a real database — there is no live
// Supabase project yet (see docs/DECISIONS.md) — so they cover the
// request-validation and no-DB-configured code paths, which run before any
// database access is attempted. The full Register->Login round trip was
// verified manually against a throwaway local Postgres container during
// development; see docs/DECISIONS.md for why that isn't wired into `go
// test` (it would make CI depend on a live database, which the plan
// explicitly avoids).

func testServer(t *testing.T) *Server {
	t.Helper()
	kp, err := jwks.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate test keypair: %v", err)
	}
	log := zap.NewNop()
	// db is intentionally nil: these tests only exercise validation and
	// the "database not configured" degrade path.
	return NewServer(nil, kp, "test-issuer", log)
}

func TestRegister_RejectsMissingFields(t *testing.T) {
	s := testServer(t)

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
	s := testServer(t)
	_, err := s.Register(context.Background(), &authv1.RegisterRequest{
		Email:    "a@example.com",
		Password: "password123",
	})
	if status.Code(err) != codes.Unavailable {
		t.Errorf("expected Unavailable when db is nil, got %v", err)
	}
}

func TestLogin_RejectsMissingFields(t *testing.T) {
	s := testServer(t)
	_, err := s.Login(context.Background(), &authv1.LoginRequest{Email: "", Password: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", err)
	}
}

func TestLogin_NoDBReturnsUnavailable(t *testing.T) {
	s := testServer(t)
	_, err := s.Login(context.Background(), &authv1.LoginRequest{
		Email:    "a@example.com",
		Password: "password123",
	})
	if status.Code(err) != codes.Unavailable {
		t.Errorf("expected Unavailable when db is nil, got %v", err)
	}
}
