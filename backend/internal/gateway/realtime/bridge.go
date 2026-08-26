package realtime

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/cache"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/rabbitmq"
)

// Bridge consumes RabbitMQ's notifications.realtime queue and, for each
// message, both republishes it onto Redis pub/sub (UserChannel) for any
// live onNotification subscriber, and appends it onto a Redis Stream
// (UserStreamKey) so a client can fetch recent history even if nothing
// was subscribed at the moment it happened. The pub/sub side is the one
// piece of plumbing that makes live delivery multi-replica-safe — any
// api-gateway replica holding that user's subscription receives it via
// Redis, regardless of which replica's RabbitMQ consumer actually
// received the original message. See the package doc comment and
// docs/DECISIONS.md's Phase 3.5 notes (pub/sub) and its Redis Streams
// notes (history).
type Bridge struct {
	redis cache.RealtimeStore
	log   *zap.Logger
}

// NewBridge constructs a Bridge. redis may be a disabled *cache.Client
// (see cache.Client.Enabled) — Publish/AppendNotification then simply log
// and drop every message, same degrade-gracefully pattern as everywhere
// else in this codebase; the Bridge itself doesn't need to know or care.
func NewBridge(redis cache.RealtimeStore, log *zap.Logger) *Bridge {
	return &Bridge{redis: redis, log: log}
}

// Handler returns a rabbitmq.Handler suitable for passing to
// rabbitmq.Consumer.Run (see cmd/api-gateway/main.go). It decodes body as
// a rabbitmq.RealtimeNotification, then, still JSON-encoded: republishes
// it onto UserChannel(notification.UserID) via Redis PUBLISH (live
// delivery) and appends it onto UserStreamKey(notification.UserID) via
// Redis XADD (history, trimmed to cache.Client's stream cap).
//
// A malformed payload (fails to decode) or one missing a user_id is
// logged at Error and the handler returns nil — treated by
// rabbitmq.Consumer.Run as "processed successfully, ack it" rather than
// nacked to a DLQ, since QueueNotificationsRealtime deliberately has none
// (see topology.go's doc comment): there is nothing useful a dead-letter
// queue would let anyone do with a malformed realtime ping days later. A
// message that decodes fine is always published and appended
// (best-effort — see cache.PubSub.Publish's and cache.Stream's doc
// comments for why a Redis-side failure in either is swallowed, not
// surfaced as a handler error).
func (b *Bridge) Handler() rabbitmq.Handler {
	return func(ctx context.Context, body []byte) error {
		var notif rabbitmq.RealtimeNotification
		if err := json.Unmarshal(body, &notif); err != nil {
			b.log.Error("failed to decode notifications.realtime message; dropping", zap.Error(err))
			return nil
		}
		if notif.UserID == "" {
			b.log.Error("notifications.realtime message missing user_id; dropping",
				zap.String("type", notif.Type))
			return nil
		}

		b.redis.Publish(ctx, UserChannel(notif.UserID), string(body))
		b.redis.AppendNotification(ctx, UserStreamKey(notif.UserID), string(body))
		b.log.Debug("republished realtime notification to redis (pub/sub + stream)",
			zap.String("user_id", notif.UserID), zap.String("type", notif.Type))
		return nil
	}
}
