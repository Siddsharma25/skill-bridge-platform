// Package cache is Phase 1c's Redis client wrapper, shared by
// skills-service/jobs-service (write-through response caching) and
// api-gateway (fixed-window rate limiting — see internal/gateway/ratelimit).
// Every method degrades gracefully rather than failing the caller's
// request: a missing REDIS_URL, an unreachable Redis, or any other
// Redis-side error is logged and treated as "cache miss" / "rate limit
// unavailable, allow the request" — the same resilience pattern this
// codebase already applies to a missing DATABASE_URL (see
// backend/CLAUDE.md and docs/DECISIONS.md). Availability wins over
// strictness here, which is a deliberate trade-off documented in
// docs/DECISIONS.md, not an oversight.
package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Cache is the read/write/invalidate subset of Client's behavior
// skills-service and jobs-service depend on for write-through response
// caching. Defined as an interface (rather than every caller taking a
// concrete *Client) specifically so a test can inject a fake in-memory
// implementation and assert things like "the DB was hit exactly once
// across two identical calls" without a real Redis instance — see
// internal/skills/server_test.go and internal/jobs/server_test.go. *Client
// implements this.
type Cache interface {
	Get(ctx context.Context, key string) (string, bool)
	Set(ctx context.Context, key, value string, ttl time.Duration)
	Del(ctx context.Context, keys ...string)
}

// RateLimiter is the subset of Client's behavior internal/gateway/ratelimit
// depends on. Split from Cache (rather than one interface with every
// method) since the gateway's rate limiter and skills/jobs-service's
// response caching are genuinely different concerns that happen to share
// one Redis connection — see docs/DECISIONS.md.
type RateLimiter interface {
	Incr(ctx context.Context, key string, window time.Duration) (count int64, ok bool)
}

// Client wraps a *redis.Client (nil when disabled) so every call site gets
// the degrade-gracefully behavior for free instead of nil-checking a raw
// Redis client itself.
type Client struct {
	rdb *redis.Client
	log *zap.Logger
}

// NewFromEnv builds a Client from REDIS_URL. If it's unset or fails to
// parse, the returned Client is still safe to use — every method becomes a
// no-op (Get always misses, Set/Del are silently skipped, Incr reports
// unavailable) — logged once here at startup rather than the process
// failing or every call site needing its own nil check.
func NewFromEnv(getenv func(string) string, log *zap.Logger) *Client {
	url := getenv("REDIS_URL")
	if url == "" {
		log.Warn("REDIS_URL not set; caching and rate limiting are disabled on this instance")
		return &Client{log: log}
	}

	opts, err := redis.ParseURL(url)
	if err != nil {
		log.Warn("REDIS_URL is set but could not be parsed; caching and rate limiting are disabled",
			zap.Error(err))
		return &Client{log: log}
	}

	return &Client{rdb: redis.NewClient(opts), log: log}
}

// Enabled reports whether this instance has a configured Redis connection.
// Every method below is safe to call regardless — this is exposed purely
// so a caller that wants to skip building a cache key/marshaling a value
// entirely can short-circuit early.
func (c *Client) Enabled() bool {
	return c != nil && c.rdb != nil
}

// Get returns (value, true) on a cache hit. A miss, a Redis-side error, or
// a disabled Client are all indistinguishable to the caller on purpose
// (false) — every caller's correct response to "not found" and "cache is
// down" is the same: fall through to the source of truth.
func (c *Client) Get(ctx context.Context, key string) (string, bool) {
	if !c.Enabled() {
		return "", false
	}
	val, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			c.log.Warn("cache get failed; falling through to source of truth",
				zap.String("key", key), zap.Error(err))
		}
		return "", false
	}
	return val, true
}

// Set stores value under key with the given TTL. Errors are logged and
// swallowed — a cache write failing must never fail the request that
// triggered it (the request already has its real answer; the cache is
// purely an optimization).
func (c *Client) Set(ctx context.Context, key, value string, ttl time.Duration) {
	if !c.Enabled() {
		return
	}
	if err := c.rdb.Set(ctx, key, value, ttl).Err(); err != nil {
		c.log.Warn("cache set failed", zap.String("key", key), zap.Error(err))
	}
}

// Del deletes zero or more keys (a no-op for an empty list). Errors are
// logged and swallowed, same reasoning as Set — a failed invalidation
// means a stale cache entry survives until its TTL expires, an accepted
// staleness window for this codebase's write-through design (see
// docs/DECISIONS.md), not a correctness bug worth failing the write over.
func (c *Client) Del(ctx context.Context, keys ...string) {
	if !c.Enabled() || len(keys) == 0 {
		return
	}
	if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
		c.log.Warn("cache del failed", zap.Strings("keys", keys), zap.Error(err))
	}
}

// Incr atomically increments key and, only on the increment that creates
// it (result == 1), sets its TTL to window — the standard Redis
// fixed-window counter pattern used by internal/gateway/ratelimit. ok is
// false if Redis is unreachable/disabled, in which case the caller must
// fail open (allow the request) rather than block it — see
// docs/DECISIONS.md's rate-limiting section for why availability wins over
// strictness in this project.
func (c *Client) Incr(ctx context.Context, key string, window time.Duration) (count int64, ok bool) {
	if !c.Enabled() {
		return 0, false
	}
	count, err := c.rdb.Incr(ctx, key).Result()
	if err != nil {
		c.log.Warn("cache incr failed; rate limiting fails open for this request",
			zap.String("key", key), zap.Error(err))
		return 0, false
	}
	if count == 1 {
		if err := c.rdb.Expire(ctx, key, window).Err(); err != nil {
			c.log.Warn("failed to set expiry on rate-limit counter", zap.String("key", key), zap.Error(err))
		}
	}
	return count, true
}

// Close closes the underlying Redis connection. Safe to call on a disabled
// Client.
func (c *Client) Close() error {
	if !c.Enabled() {
		return nil
	}
	return c.rdb.Close()
}
