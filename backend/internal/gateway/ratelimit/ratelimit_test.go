package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/authctx"
)

// fakeLimiter is a minimal in-memory stand-in for cache.RateLimiter. Unlike
// a real Redis-backed counter it never expires a window on its own —
// resetWindow (called by the "recovers after the window elapses" test)
// simulates that instead, so the test doesn't need a real sleep longer
// than the configured window.
type fakeLimiter struct {
	mu     sync.Mutex
	counts map[string]int64
	// unavailable, when true, makes every Incr report ok=false — simulating
	// Redis being unreachable, the scenario the "fails open" test exercises.
	unavailable bool
}

func newFakeLimiter() *fakeLimiter {
	return &fakeLimiter{counts: make(map[string]int64)}
}

func (f *fakeLimiter) Incr(_ context.Context, key string, _ time.Duration) (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return 0, false
	}
	f.counts[key]++
	return f.counts[key], true
}

func (f *fakeLimiter) resetWindow() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts = make(map[string]int64)
}

func noopHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func testLogger(t *testing.T) *zap.Logger {
	t.Helper()
	log, err := zap.NewDevelopment()
	if err != nil {
		t.Fatalf("failed to build test logger: %v", err)
	}
	return log
}

// TestMiddleware_RejectsOnceLimitExceeded is the proof the task specifically
// calls for: a client making more than cfg.Limit requests within cfg.Window
// gets a 429 on the request that pushes it over, not before and not
// instead of every request after.
func TestMiddleware_RejectsOnceLimitExceeded(t *testing.T) {
	limiter := newFakeLimiter()
	cfg := Config{Limit: 3, Window: time.Minute}
	handler := Middleware(limiter, cfg, testLogger(t))(noopHandler())

	doRequest := func() int {
		req := httptest.NewRequest(http.MethodPost, "/query", nil)
		req.RemoteAddr = "203.0.113.10:54321"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 1; i <= 3; i++ {
		if code := doRequest(); code != http.StatusOK {
			t.Fatalf("request %d: expected 200 within limit, got %d", i, code)
		}
	}

	// The 4th request from the same key within the same window must be
	// rejected.
	if code := doRequest(); code != http.StatusTooManyRequests {
		t.Fatalf("request 4: expected 429 once over limit, got %d", code)
	}
	// And so must a 5th — this isn't a one-shot rejection.
	if code := doRequest(); code != http.StatusTooManyRequests {
		t.Fatalf("request 5: expected 429 to persist within the same window, got %d", code)
	}
}

// TestMiddleware_RecoversAfterWindowElapses proves the fixed-window design
// actually resets rather than permanently banning a key once it's tripped
// the limit once — simulated via fakeLimiter.resetWindow rather than a real
// sleep, since the middleware itself has no special-cased "window expired"
// logic of its own; it just trusts whatever count the limiter (Redis, via
// its own key TTL, in production) reports.
func TestMiddleware_RecoversAfterWindowElapses(t *testing.T) {
	limiter := newFakeLimiter()
	cfg := Config{Limit: 1, Window: time.Minute}
	handler := Middleware(limiter, cfg, testLogger(t))(noopHandler())

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/query", nil)
		r.RemoteAddr = "198.51.100.5:1234"
		return r
	}

	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req())
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req())
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: expected 429 (over limit of 1), got %d", rec2.Code)
	}

	// Simulate the fixed window elapsing (Redis's key TTL expiring the
	// counter in production).
	limiter.resetWindow()

	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req())
	if rec3.Code != http.StatusOK {
		t.Fatalf("request after window reset: expected 200, got %d", rec3.Code)
	}
}

// TestMiddleware_FailsOpenWhenLimiterUnavailable is the other proof the task
// specifically calls for: a Redis-down scenario (limiter.Incr reporting
// ok=false, exactly what cache.Client.Incr returns when its Redis
// connection is unreachable) must never block a request — this project's
// established resilience pattern (see docs/DECISIONS.md) is "degrade the
// feature, never fail the request" for every Redis-backed capability.
func TestMiddleware_FailsOpenWhenLimiterUnavailable(t *testing.T) {
	limiter := newFakeLimiter()
	limiter.unavailable = true
	cfg := Config{Limit: 1, Window: time.Minute}
	handler := Middleware(limiter, cfg, testLogger(t))(noopHandler())

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/query", nil)
		r.RemoteAddr = "192.0.2.7:9999"
		return r
	}

	// Fire well more requests than the configured limit — every single one
	// must still succeed, since the limiter can't count them at all.
	for i := 1; i <= 10; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req())
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d with limiter unavailable: expected 200 (fail open), got %d", i, rec.Code)
		}
	}
}

// TestRateLimitKey_PrefersUserIDOverIP proves the composite-key design
// documented on rateLimitKey: once authctx has verified a caller, that
// identity — not the network address — is what gets rate-limited, so two
// requests from the same IP but different verified users don't share one
// budget, and the reverse (same user, different IP) does share one.
func TestRateLimitKey_PrefersUserIDOverIP(t *testing.T) {
	limiter := newFakeLimiter()
	cfg := Config{Limit: 1, Window: time.Minute}
	handler := Middleware(limiter, cfg, testLogger(t))(noopHandler())

	makeReq := func(remoteAddr string, userID string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/query", nil)
		r.RemoteAddr = remoteAddr
		if userID != "" {
			r = r.WithContext(authctx.NewContext(r.Context(), userID, "user"))
		}
		return r
	}

	// Same IP, two different authenticated users: each gets their own
	// budget, so both of these first requests must succeed.
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, makeReq("203.0.113.99:1111", "user-a"))
	if rec1.Code != http.StatusOK {
		t.Fatalf("user-a first request: expected 200, got %d", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, makeReq("203.0.113.99:1111", "user-b"))
	if rec2.Code != http.StatusOK {
		t.Fatalf("user-b first request (same IP as user-a): expected 200 (separate budget), got %d", rec2.Code)
	}

	// user-a again, from a different IP this time: still counts against
	// user-a's own budget, which is now exhausted (limit 1).
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, makeReq("198.51.100.42:2222", "user-a"))
	if rec3.Code != http.StatusTooManyRequests {
		t.Fatalf("user-a second request (different IP): expected 429 (same user budget), got %d", rec3.Code)
	}
}
