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
