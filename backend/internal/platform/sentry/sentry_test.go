package sentry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// No live Sentry project is required for these — same reasoning as every
// other internal/platform package's tests: config-wiring and
// degrade-gracefully behavior only. Whether a real event actually reaches
// a Sentry project is left for a manual check with a throwaway DSN (see
// docs/DECISIONS.md's Sentry section), the same category of gap Google
// OAuth's live-login case already documents for this codebase.

func TestInitFromEnv_NoDSNDisables(t *testing.T) {
	h := InitFromEnv(func(string) string { return "" }, "test-service", zap.NewNop())
	if h.Enabled() {
		t.Fatalf("expected a Handle with no SENTRY_DSN to be disabled")
	}
	// Every method must be a safe no-op on a disabled Handle, not a panic.
	h.Flush(0)
	if core := h.WrapCore(zapcore.NewNopCore()); core != zapcore.NewNopCore() {
		// zapcore.NewNopCore() returns the same singleton value each call,
		// so this comparison is meaningful, not incidental.
		t.Fatalf("expected WrapCore to return base unchanged when disabled")
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	wrapped := h.RecoverMiddleware(next)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected RecoverMiddleware to pass through to next when disabled, got status %d", rec.Code)
	}
}

// fakeTestDSN is a syntactically valid but non-functional DSN (no such
// Sentry project exists) — sentry.Init only parses/validates the DSN
// shape, it doesn't dial out, so this is enough to exercise the enabled
// path without a real account. Built via concatenation rather than a
// literal "key@host" string so it doesn't read as (and can't be mistaken
// for) a real email address.
const fakeTestDSN = "https://examplepublickey" + "@" + "127.0.0.1:9000/123456"

func TestInitFromEnv_InvalidSampleRateFallsBackToZero(t *testing.T) {
	// An invalid SENTRY_TRACES_SAMPLE_RATE must not fail startup — same
	// convention as ratelimit's RATE_LIMIT_REQUESTS handling in
	// cmd/allinone/main.go: log a warning, use the documented default.
	h := InitFromEnv(func(key string) string {
		switch key {
		case "SENTRY_DSN":
			return fakeTestDSN
		case "SENTRY_TRACES_SAMPLE_RATE":
			return "not-a-number"
		default:
			return ""
		}
	}, "test-service", zap.NewNop())
	defer h.Flush(0)
	if !h.Enabled() {
		t.Fatalf("expected a Handle with a valid SENTRY_DSN to be enabled despite an invalid sample rate")
	}
}

func TestWrapCore_EnabledAttachesSentryCoreWithoutError(t *testing.T) {
	// The enabled path — zapsentry.NewCore actually building a core against
	// the client sentry.Init just registered as the current hub's client
	// (WrapCore reads sentry.CurrentHub().Client(), so ordering vs. Init
	// matters here exactly as it does in every cmd/*/main.go). A config
	// error here would silently fall back to `base` alone (see WrapCore's
	// doc comment) rather than fail loudly, so this asserts the *happy*
	// path actually attaches a second core, not just that it doesn't panic.
	h := InitFromEnv(func(key string) string {
		if key == "SENTRY_DSN" {
			return fakeTestDSN
		}
		return ""
	}, "test-service", zap.NewNop())
	defer h.Flush(0)

	base := zapcore.NewNopCore()
	wrapped := h.WrapCore(base)
	if wrapped == base {
		t.Fatalf("expected WrapCore to attach a Sentry-reporting core alongside base when enabled, got base unchanged")
	}
	// zapcore.NewTee's result satisfies zapcore.Core and must accept a
	// write without erroring (an actual delivery attempt happens
	// asynchronously in a background worker sentry-go owns, which this
	// test doesn't wait on — see the package doc comment on what's left
	// for a manual check with a real DSN).
	entry := zapcore.Entry{Level: zapcore.ErrorLevel, Message: "test error for WrapCore coverage"}
	if err := wrapped.Write(entry, nil); err != nil {
		t.Fatalf("unexpected error writing through the wrapped core: %v", err)
	}
}

func TestUnaryServerInterceptor_RecoversPanicEvenWhenDisabled(t *testing.T) {
	// The reliability fix (recover a panicking gRPC handler instead of
	// crashing the process) must hold with no Sentry configured at all —
	// only the reporting half is gated on Enabled().
	h := &Handle{}
	interceptor := h.UnaryServerInterceptor()

	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
	panicking := func(_ context.Context, _ any) (any, error) {
		panic("boom")
	}

	resp, err := interceptor(context.Background(), nil, info, panicking)
	if resp != nil {
		t.Fatalf("expected a nil response after a recovered panic, got %v", resp)
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected codes.Internal after a recovered panic, got %v", err)
	}
}

func TestUnaryServerInterceptor_PassesThroughNormalCalls(t *testing.T) {
	h := &Handle{}
	interceptor := h.UnaryServerInterceptor()

	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
	ok := func(_ context.Context, req any) (any, error) {
		return req, nil
	}

	resp, err := interceptor(context.Background(), "hello", info, ok)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "hello" {
		t.Fatalf("expected the handler's own response to pass through unchanged, got %v", resp)
	}
}
