package authctx

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/jwks"
)

// TestVerifyToken_ValidTokenReturnsSubject proves the seam both
// authctx.Middleware (HTTP) and cmd/api-gateway/main.go's WebSocket
// connection_init handler (Phase 3.5) share: a token signed by
// auth-service and served over a real JWKS endpoint verifies to its
// subject claim through this one function.
func TestVerifyToken_ValidTokenReturnsSubject(t *testing.T) {
	kp, err := jwks.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	tokenStr, err := jwks.Sign(kp, "test-issuer", "user-123", "user@example.com", "user", time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	srv := httptest.NewServer(jwks.Handler(kp))
	defer srv.Close()
	client := jwks.NewClient(srv.URL, time.Minute)

	userID, role, err := VerifyToken(t.Context(), client, tokenStr)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if userID != "user-123" {
		t.Errorf("expected user-123, got %q", userID)
	}
	if role != "user" {
		t.Errorf("expected role %q, got %q", "user", role)
	}
}

// TestVerifyToken_InvalidTokenErrors is the negative case both the HTTP
// middleware and the WebSocket connection_init handler depend on to
// reject a bad token rather than silently treating it as unauthenticated
// success — see cmd/api-gateway/main.go's wsInitFunc, which returns this
// error (closing the connection) rather than falling through the way
// Middleware does for an unrelated public HTTP query.
func TestVerifyToken_InvalidTokenErrors(t *testing.T) {
	kp, err := jwks.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	srv := httptest.NewServer(jwks.Handler(kp))
	defer srv.Close()
	client := jwks.NewClient(srv.URL, time.Minute)

	if _, _, err := VerifyToken(t.Context(), client, "not-a-jwt-at-all"); err == nil {
		t.Fatal("expected an error for a garbage token")
	}
}

func TestVerifyToken_EmptyTokenErrors(t *testing.T) {
	kp, err := jwks.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	srv := httptest.NewServer(jwks.Handler(kp))
	defer srv.Close()
	client := jwks.NewClient(srv.URL, time.Minute)

	if _, _, err := VerifyToken(t.Context(), client, ""); err == nil {
		t.Fatal("expected an error for an empty token")
	}
}

func TestNewContextAndUserID_RoundTrip(t *testing.T) {
	ctx := NewContext(t.Context(), "user-123", "admin")
	id, ok := UserID(ctx)
	if !ok || id != "user-123" {
		t.Fatalf("expected (user-123, true), got (%q, %v)", id, ok)
	}
	role, ok := Role(ctx)
	if !ok || role != "admin" {
		t.Fatalf("expected (admin, true), got (%q, %v)", role, ok)
	}
}

func TestUserID_AbsentContextReturnsFalse(t *testing.T) {
	if _, ok := UserID(t.Context()); ok {
		t.Fatal("expected ok=false when no user ID was stored in context")
	}
}

func TestRole_AbsentContextReturnsFalse(t *testing.T) {
	if _, ok := Role(t.Context()); ok {
		t.Fatal("expected ok=false when no role was stored in context")
	}
}
