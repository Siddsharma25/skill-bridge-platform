package graph

import (
	"context"
	"testing"

	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/authctx"
)

func TestRequireAdmin_NoCallerReturnsAuthenticationRequired(t *testing.T) {
	_, err := requireAdmin(context.Background())
	if err == nil {
		t.Fatal("expected an error for an unauthenticated context")
	}
	if got := err.Error(); got != "authentication required" {
		t.Errorf("expected the same message requireUserID uses, got %q", got)
	}
}

func TestRequireAdmin_AuthenticatedNonAdminReturnsAdminRoleRequired(t *testing.T) {
	ctx := authctx.NewContext(context.Background(), "user-1", "user")
	_, err := requireAdmin(ctx)
	if err == nil {
		t.Fatal("expected an error for a non-admin caller")
	}
	if got := err.Error(); got != "admin role required" {
		t.Errorf("expected a distinct 'admin role required' message, got %q", got)
	}
}

func TestRequireAdmin_AdminCallerSucceeds(t *testing.T) {
	ctx := authctx.NewContext(context.Background(), "admin-1", "admin")
	userID, err := requireAdmin(ctx)
	if err != nil {
		t.Fatalf("expected no error for an admin caller, got %v", err)
	}
	if userID != "admin-1" {
		t.Errorf("expected requireAdmin to return the caller's user ID, got %q", userID)
	}
}
