package rabbitmq

import "time"

// EventTypeWelcome is the only EmailNotification.EventType value produced
// today — auth-service's Register publishes it once per successful
// registration. A plain string const (not an enum type) matches
// internal/platform/kafka/events.go's style, which is deliberately loose
// here since the JSON envelope, not a generated type, is the actual
// contract crossing the Go/TypeScript boundary.
const EventTypeWelcome = "welcome"

// EmailNotification is QueueNotificationsEmail's payload — a plain
// JSON-tagged struct, not a protobuf message, mirroring
// internal/platform/kafka/events.go's envelope style/reasoning (see that
// file's package doc for why JSON was chosen over protobuf for broker
// payloads despite this project being proto-first for gRPC): it also
// crosses a language boundary here (Go producer, TypeScript consumer),
// where a shared .proto would need codegen wired into notification-service
// too for little benefit over a documented JSON shape.
//
// MessageID is the idempotency key notification-service's notification_log
// dedupes on (a UNIQUE constraint — see
// backend/migrations/notifications/00001_create_notification_log.sql): at
// -least-once delivery (a redelivered message after a consumer restart, or
// a manual republish during debugging) must not double-log the same
// notification.
type EmailNotification struct {
	MessageID string    `json:"message_id"`
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	EventType string    `json:"event_type"`
	CreatedAt time.Time `json:"created_at"`
}

// RealtimeNotificationTypeWelcome is published by auth-service's Register
// (see internal/auth/server.go) — the architecturally-complete producer for
// this event type, but NOT this phase's live-verification trigger: the
// publish happens synchronously inside Register, before the newly
// registered user could possibly have opened a WebSocket subscription (no
// JWT exists to authenticate one with until Register returns), and Redis
// pub/sub has no replay buffer — a publish with nobody subscribed is simply
// lost. See docs/DECISIONS.md's Phase 3.5 notes for the full reasoning and
// why job.matched (RealtimeNotificationTypeJobMatch) is used for live
// verification instead.
const RealtimeNotificationTypeWelcome = "welcome"

// RealtimeNotificationTypeJobMatch is published by jobs-service's matching
// worker (internal/jobs/matcher.go's HandleJobPosted) once per user it
// upserts a jobs.job_matches row for — this phase's live-verification
// trigger, since a user can register, log in, and open a genuinely
// listening onNotification subscription before a job matching their
// skills is created, unlike the registration-time welcome notification
// above. See docs/DECISIONS.md.
const RealtimeNotificationTypeJobMatch = "job_match"

// RealtimeNotification is QueueNotificationsRealtime's payload (Phase
// 3.5) — a plain JSON-tagged struct, same reasoning as EmailNotification
// above (crosses no language boundary here, but keeping every broker
// payload in this codebase JSON rather than protobuf is the established,
// documented convention — see docs/DECISIONS.md's Phase 2 notes on event
// payload format).
//
// api-gateway's realtime bridge (internal/gateway/realtime) decodes this,
// republishes it verbatim (still JSON-encoded) onto Redis pub/sub keyed by
// UserID, and the onNotification GraphQL subscription resolver decodes it
// a second time into the GraphQL Notification type. JobID/Score are only
// populated for EventType == RealtimeNotificationTypeJobMatch — `omitempty`
// keeps the welcome payload's JSON free of meaningless zero values.
type RealtimeNotification struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
	JobID     string    `json:"job_id,omitempty"`
	Score     float64   `json:"score,omitempty"`
}
