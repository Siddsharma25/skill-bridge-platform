// Package cache is Phase 1c's Redis client wrapper, shared by
// skills-service/jobs-service (write-through response caching),
// api-gateway (fixed-window rate limiting — see internal/gateway/ratelimit),
// and, as of Phase 3.5, api-gateway's realtime notification bridge and
// onNotification GraphQL subscription (pub/sub fan-out — see
// internal/gateway/realtime and docs/DECISIONS.md's Phase 3.5 notes).
// Every method degrades gracefully rather than failing the caller's
// request: a missing REDIS_URL, an unreachable Redis, or any other
// Redis-side error is logged and treated as "cache miss" / "rate limit
// unavailable, allow the request" / "no live subscription available" —
// the same resilience pattern this codebase already applies to a missing
// DATABASE_URL (see backend/CLAUDE.md and docs/DECISIONS.md).
// Availability wins over strictness here, which is a deliberate trade-off
// documented in docs/DECISIONS.md, not an oversight.
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

// PubSub is the subset of Client's behavior Phase 3.5's realtime
// notification bridge depends on: api-gateway's RabbitMQ consumer
// (internal/gateway/realtime.Bridge) republishes onto a Redis channel via
// Publish, and the onNotification GraphQL subscription resolver
// (schema.resolvers.go) opens a Subscribe on its caller's own channel.
// Split from Cache/RateLimiter for the same reason those two are split
// from each other — a genuinely different concern sharing the same Redis
// connection, not because the underlying client differs. Defined as an
// interface, same reasoning as Cache/RateLimiter, so a test can inject a
// fake and assert "exactly one message was published to this channel"
// without a real Redis instance.
type PubSub interface {
	// Publish publishes message on channel via Redis PUBLISH. Best-effort:
	// like Set/Del, a failure (including Redis being disabled entirely) is
	// logged and swallowed rather than propagated — the caller (the
	// realtime bridge) has no primary write of its own to protect here;
	// this *is* the whole operation, and Redis pub/sub already has no
	// delivery guarantee even when it succeeds (see
	// docs/DECISIONS.md's Phase 3.5 notes), so there is nothing a returned
	// error would let the caller usefully do differently.
	Publish(ctx context.Context, channel, message string)
	// Subscribe opens a Redis pub/sub subscription on channel. ok is false
	// when Redis is disabled/unreachable — the caller (the onNotification
	// resolver) must treat that as "no live notifications available on
	// this instance" (e.g. a clear GraphQL subscription error) rather than
	// blocking forever or panicking, same degrade-gracefully convention as
	// Cache.Get/RateLimiter.Incr reporting a miss/unavailable instead of
	// erroring.
	Subscribe(ctx context.Context, channel string) (Subscription, bool)
}

// Stream is the subset of Client's behavior backing a per-user bounded
// notification history — a Redis Stream, not the pub/sub channel PubSub
// wraps. Pub/sub has zero delivery guarantee and no memory: a client that
// isn't subscribed at the moment a notification is published never sees
// it, including on every page refresh (the frontend's documented "gone on
// refresh" gap — see frontend/README.md). A Stream is an append-only log
// Redis retains (up to a trim limit) independent of who's listening, so a
// client can ask "what did I miss" on load instead of only "tell me what
// happens next." Split from PubSub for the same reason PubSub is split
// from Cache/RateLimiter: a genuinely different Redis primitive (XADD/
// XREVRANGE vs PUBLISH/SUBSCRIBE) sharing one connection, not a different
// underlying client.
type Stream interface {
	// AppendNotification appends payload (typically the same JSON already
	// being published to PubSub) to the stream at key via XADD, trimmed
	// to approximately the most recent streamMaxLen entries (MAXLEN ~ —
	// approximate trimming, not exact: Redis trims lazily in whole radix
	// tree nodes rather than evicting precisely one entry per add, which
	// is the standard production trade-off — see cache.go's
	// streamMaxLen doc comment for why exactness isn't worth its extra
	// cost here). Best-effort, same swallow-and-log convention as
	// Publish: a failed history write must never fail the realtime bridge
	// that's also delivering this notification live.
	AppendNotification(ctx context.Context, key, payload string)
	// RecentNotifications returns up to limit of the most recently
	// appended payloads at key, newest first (XREVRANGE). ok is false
	// when Redis is disabled/unreachable, same "unavailable is a
	// distinct, checkable return" convention as Subscribe/Incr — the
	// caller (the notificationHistory resolver) must treat that as "no
	// history available on this instance," not an empty-but-successful
	// result.
	RecentNotifications(ctx context.Context, key string, limit int64) ([]string, bool)
}

// RealtimeStore combines PubSub and Stream — the full set of Redis
// capabilities api-gateway's realtime notification path needs: live
// fan-out for the onNotification subscription, and a bounded history for
// the notificationHistory query. *Client implements both, so one Client
// value satisfies this without any adapter code; a test that only cares
// about one half can still implement just PubSub or just Stream directly
// where only that interface is asked for (see realtime/bridge_test.go's
// fakePubSub, which is *not* asked to also implement Stream anywhere it
// doesn't need to).
type RealtimeStore interface {
	PubSub
	Stream
}

// Subscription is the minimal handle Subscribe hands back — satisfied
// directly by *redis.PubSub (its Channel/Close methods already match this
// shape), and by a fake in tests. Closing it is what releases the
// underlying Redis subscription; the onNotification resolver's cleanup
// path (ctx.Done()/client disconnect) must always call Close exactly
// once, or a dropped WebSocket client leaks a live Redis subscription
// forever — the specific leak Phase 3.5's task called out to test for.
type Subscription interface {
	// Channel matches *redis.PubSub's own signature (a variadic
	// redis.ChannelOption, unused by every caller in this codebase today)
	// rather than a narrower Channel() with no arguments, so *redis.PubSub
	// satisfies this interface with zero adapter code.
	Channel(opts ...redis.ChannelOption) <-chan *redis.Message
	Close() error
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

// Publish implements PubSub.Publish. See that interface's doc comment for
// why a failure (or Redis being disabled) is logged and swallowed rather
// than returned.
func (c *Client) Publish(ctx context.Context, channel, message string) {
	if !c.Enabled() {
		c.log.Warn("pubsub disabled (no REDIS_URL); dropping realtime publish", zap.String("channel", channel))
		return
	}
	if err := c.rdb.Publish(ctx, channel, message).Err(); err != nil {
		c.log.Warn("redis publish failed; realtime notification will not be delivered",
			zap.String("channel", channel), zap.Error(err))
	}
}

// Subscribe implements PubSub.Subscribe. Returning (nil, false) rather
// than a non-nil *redis.PubSub with no live connection matches
// Cache.Get/RateLimiter.Incr's "unavailable is a distinct, checkable
// return, never a partially-usable zero value" convention elsewhere in
// this file.
func (c *Client) Subscribe(ctx context.Context, channel string) (Subscription, bool) {
	if !c.Enabled() {
		return nil, false
	}
	return c.rdb.Subscribe(ctx, channel), true
}

// streamMaxLen caps how many recent notifications a per-user stream
// retains. Approximate trimming (see AppendNotification) means Redis may
// keep somewhat more than this at any instant, but it converges back down
// on the next add — fine for a "recent history" feature where the exact
// cutoff has never mattered to any caller, only "roughly this many."
const streamMaxLen = 50

// AppendNotification implements Stream.AppendNotification.
func (c *Client) AppendNotification(ctx context.Context, key, payload string) {
	if !c.Enabled() {
		c.log.Warn("stream disabled (no REDIS_URL); notification history will not include this entry",
			zap.String("key", key))
		return
	}
	err := c.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		MaxLen: streamMaxLen,
		Approx: true,
		Values: map[string]any{streamPayloadField: payload},
	}).Err()
	if err != nil {
		c.log.Warn("redis XADD failed; notification history will not include this entry",
			zap.String("key", key), zap.Error(err))
	}
}

// streamPayloadField is the single field name every stream entry stores
// its JSON payload under — one field per entry is enough here (unlike a
// Redis Hash's natural multi-field use), so a fixed constant name rather
// than a caller-supplied one keeps AppendNotification/RecentNotifications
// from needing to agree on it out of band.
const streamPayloadField = "payload"

// RecentNotifications implements Stream.RecentNotifications.
func (c *Client) RecentNotifications(ctx context.Context, key string, limit int64) ([]string, bool) {
	if !c.Enabled() {
		return nil, false
	}
	msgs, err := c.rdb.XRevRangeN(ctx, key, "+", "-", limit).Result()
	if err != nil {
		c.log.Warn("redis XREVRANGE failed; notification history unavailable",
			zap.String("key", key), zap.Error(err))
		return nil, false
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if payload, ok := m.Values[streamPayloadField].(string); ok {
			out = append(out, payload)
		}
	}
	return out, true
}

// Close closes the underlying Redis connection. Safe to call on a disabled
// Client.
func (c *Client) Close() error {
	if !c.Enabled() {
		return nil
	}
	return c.rdb.Close()
}
