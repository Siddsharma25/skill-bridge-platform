package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoadAllowedOriginsFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"unset falls back to default", map[string]string{}, DefaultAllowedOrigins},
		{"blank falls back to default", map[string]string{"CORS_ALLOWED_ORIGINS": "   "}, DefaultAllowedOrigins},
		{
			"single origin",
			map[string]string{"CORS_ALLOWED_ORIGINS": "https://skill-bridge.vercel.app"},
			[]string{"https://skill-bridge.vercel.app"},
		},
		{
			"multiple, comma-separated, whitespace trimmed",
			map[string]string{"CORS_ALLOWED_ORIGINS": " https://skill-bridge.vercel.app , http://localhost:5173 "},
			[]string{"https://skill-bridge.vercel.app", "http://localhost:5173"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LoadAllowedOriginsFromEnv(func(k string) string { return tt.env[k] })
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestMiddleware_AllowedOriginGetsEchoedBack(t *testing.T) {
	mw := Middleware([]string{"https://skill-bridge.vercel.app", "http://localhost:5173"}, nil)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()

	mw(next).ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called for an allowed origin")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the matching origin echoed back, not a wildcard", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary = %q, want %q", got, "Origin")
	}
}

func TestMiddleware_DisallowedOriginGetsNoCORSHeaders(t *testing.T) {
	mw := Middleware([]string{"https://skill-bridge.vercel.app"}, nil)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()

	mw(next).ServeHTTP(rec, req)

	// A disallowed origin still reaches the handler (this middleware
	// doesn't reject the request — see the doc comment: the browser's own
	// same-origin enforcement is what actually blocks the response from
	// being read, since a non-browser caller with no Origin header must
	// keep working unaffected).
	if !called {
		t.Fatal("expected next handler to still be called; rejection isn't this middleware's job")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty for a disallowed origin", got)
	}
}

func TestMiddleware_NoOriginHeaderPassesThroughUnaffected(t *testing.T) {
	mw := Middleware([]string{"http://localhost:5173"}, nil)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// No Origin header at all — a same-origin request, curl, grpcurl-style
	// debugging, or a server-to-server call, none of which should be
	// affected by CORS logic in any way.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	mw(next).ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called for a request with no Origin header")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty when no Origin header was sent", got)
	}
}

func TestMiddleware_PreflightIsHandledDirectly(t *testing.T) {
	mw := Middleware([]string{"http://localhost:5173"}, nil)
	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})

	req := httptest.NewRequest(http.MethodOptions, "/query", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()

	mw(next).ServeHTTP(rec, req)

	if called {
		t.Fatal("a preflight OPTIONS request should never reach the wrapped handler")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight response code = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("expected Access-Control-Allow-Methods to be set on a preflight response for an allowed origin")
	}
}
