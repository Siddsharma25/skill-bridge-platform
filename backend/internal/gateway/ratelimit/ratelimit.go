// Package ratelimit implements a Redis-backed fixed-window rate limiter as
// HTTP middleware in front of api-gateway's GraphQL endpoint (see
// cmd/api-gateway/main.go). See docs/DECISIONS.md for why fixed-window
// (not token-bucket) was chosen and why a rejected request gets a
// transport-level HTTP 429 rather than a GraphQL-layer error the way
// authctx's requireUserID does.
package ratelimit

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/authctx"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/cache"
)

// Config controls the fixed window's size and per-key request budget.
type Config struct {
	// Limit is the number of requests one key may make within Window
	// before subsequent requests are rejected. Zero means "use
	// DefaultConfig.Limit."
	Limit int64
	// Window is the fixed-window duration. Zero means "use
	// DefaultConfig.Window."
	Window time.Duration
}

// DefaultConfig is 100 requests per key per minute. Generous enough not to
// bother a normal interactive GraphQL client (Playground, a frontend
// re-fetching occasionally) while still bounding a runaway loop or
// scripted abuse — see docs/DECISIONS.md for the reasoning and for how
// this can be tuned via RATE_LIMIT_REQUESTS/RATE_LIMIT_WINDOW_SECONDS
// (cmd/api-gateway/main.go) without a code change.
var DefaultConfig = Config{Limit: 100, Window: time.Minute}

// Middleware enforces cfg against limiter, keyed primarily by the caller's
// verified user ID (authctx.UserID) when the request carries one, falling
// back to client IP otherwise — see rateLimitKey and docs/DECISIONS.md for
// why this project rate-limits "the identity that made the request" rather
// than always the network address alone. Must run after
// authctx.Middleware in the handler chain (see cmd/api-gateway/main.go) so
// authctx.UserID(ctx) is already populated by the time this runs.
//
// Degrades open: if limiter reports its backing Redis is unreachable (the
// bool return from Incr is false), the request is allowed through and
// logged rather than rejected — the same resilience pattern as
// cache.Cache elsewhere in this codebase (a missing/unreachable Redis
// disables caching and rate limiting, it never fails the request — see
// docs/DECISIONS.md).
func Middleware(limiter cache.RateLimiter, cfg Config, log *zap.Logger) func(http.Handler) http.Handler {
	if cfg.Limit <= 0 {
		cfg.Limit = DefaultConfig.Limit
	}
	if cfg.Window <= 0 {
		cfg.Window = DefaultConfig.Window
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := rateLimitKey(r)
			count, ok := limiter.Incr(r.Context(), key, cfg.Window)
			if !ok {
				log.Warn("rate limiter unavailable (redis unreachable/disabled); allowing request through",
					zap.String("key", key))
				next.ServeHTTP(w, r)
				return
			}
			if count > cfg.Limit {
				log.Info("rejecting request over rate limit",
					zap.String("key", key), zap.Int64("count", count), zap.Int64("limit", cfg.Limit))
				w.Header().Set("Retry-After", strconv.Itoa(int(cfg.Window.Seconds())))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				// A plain JSON body (not a graphql-response-shaped one) is
				// deliberate: this rejection happens before the GraphQL
				// handler ever sees the request, so there is no GraphQL
				// operation to attach a "data: null, errors: [...]"
				// envelope to. See the package doc comment.
				_, _ = fmt.Fprintf(w, `{"error":"rate limit exceeded, retry after %d seconds"}`, int(cfg.Window.Seconds()))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// rateLimitKey builds the Redis key this request counts against:
// "ratelimit:user:<id>" for an authenticated caller (see authctx.UserID),
// else "ratelimit:ip:<addr>". Preferring user ID once known avoids two
// failure modes a pure IP key would have: multiple users behind one
// NAT/corporate-proxy sharing a single budget, and one user rotating IPs
// to dodge the limit. An unauthenticated caller only ever has an IP to be
// identified by, so that's the fallback.
func rateLimitKey(r *http.Request) string {
	if userID, ok := authctx.UserID(r.Context()); ok {
		return "ratelimit:user:" + userID
	}
	return "ratelimit:ip:" + clientIP(r)
}

// clientIP extracts the caller's address: the first entry of
// X-Forwarded-For if present (this gateway may sit behind a proxy/load
// balancer once deployed — see docs/DECISIONS.md's production-deployment
// notes), otherwise RemoteAddr's host part (RemoteAddr is "host:port";
// SplitHostPort strips the port, which would otherwise make every request
// from the same client count against a different key).
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if before, _, found := strings.Cut(fwd, ","); found {
			return strings.TrimSpace(before)
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
