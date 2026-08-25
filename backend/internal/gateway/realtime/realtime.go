// Package realtime is Phase 3.5's bridge between RabbitMQ's
// notifications.realtime queue and Redis pub/sub, plus the small helper
// every producer/consumer of a user's live-notification channel shares
// (channel naming). See docs/DECISIONS.md's Phase 3.5 notes for the full
// design and why the RabbitMQ hop is kept deliberately rather than
// skipped in favor of publishing straight to Redis from auth-service/
// jobs-service.
//
// The flow this package sits in the middle of:
//
//	auth-service / jobs-service --(AMQP)--> notifications.realtime queue
//	    --(this package's Bridge, running inside api-gateway)-->
//	    Redis PUBLISH on UserChannel(user_id)
//	    --(Redis SUBSCRIBE, one per open onNotification GraphQL
//	       subscription — see internal/gateway/graph/schema.resolvers.go)-->
//	    the connected WebSocket client
//
// Redis pub/sub has no replay buffer: a PUBLISH with no live SUBSCRIBE on
// that channel at that instant is simply lost. That's a deliberate,
// documented characteristic of this design (see docs/DECISIONS.md), not a
// bug in this package — it's what makes jobs-service's job.matched
// publish (not auth-service's registration-time publish) the right choice
// for proving the subscription live, since only the former can reliably
// have a subscriber already listening.
package realtime

// UserChannel returns the Redis pub/sub channel name a given user's live
// notifications are published to and subscribed from. Both this package's
// Bridge (publishing) and the onNotification GraphQL subscription
// resolver (subscribing) call this rather than each formatting the string
// independently, so the two sides can never drift out of sync.
func UserChannel(userID string) string {
	return "realtime:user:" + userID
}
