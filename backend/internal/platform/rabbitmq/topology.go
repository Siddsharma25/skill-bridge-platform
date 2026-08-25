// Package rabbitmq is Phase 3's thin wrapper around a real AMQP client
// (github.com/rabbitmq/amqp091-go — the maintained fork of the archived
// streadway/amqp; see docs/DECISIONS.md's "Phase 3 implementation notes"),
// giving every service the same degrade-gracefully-when-unconfigured shape
// internal/platform/kafka gives them for Kafka: a Producer that never fails
// the RPC that triggered it, just logs and moves on.
//
// Unlike Kafka's auto-created topics, RabbitMQ queues bound to the default
// exchange must exist before a publish reaches them (a publish to a
// non-existent queue is silently dropped, not an error) — and this
// codebase wants a dead-letter exchange wired up, which only exists if
// something declares it. This file holds that topology declaration, shared
// verbatim (by name and by argument) between the Go producer side
// (auth-service, via NewProducerFromEnv) and the NestJS consumer side
// (notification-service, via its own equivalent declaration — see
// backend/notification-service/src/rabbitmq/rabbitmq.module.ts). AMQP's
// queue.declare/exchange.declare are idempotent as long as every caller
// declares the exact same arguments, so both sides declaring it — rather
// than picking one "owner" and hoping it always starts first — is what
// actually avoids the "publish happens before the queue exists yet"
// message-loss hazard in local dev, where startup order between
// auth-service and notification-service isn't guaranteed. See
// docs/DECISIONS.md for the fuller reasoning and why this diverges from
// the plan's "pick one side" framing.
package rabbitmq

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	// QueueNotificationsEmail carries one EmailNotification JSON payload
	// per message, published by auth-service's Register and consumed by
	// notification-service. Declared with a dead-letter-exchange argument
	// pointing at ExchangeNotificationsDLX, so a message notification-service
	// nacks without requeue lands in QueueNotificationsEmailDLQ instead of
	// being requeued forever or silently discarded.
	QueueNotificationsEmail = "notifications.email"

	// ExchangeNotificationsDLX is the dead-letter exchange every
	// dead-letterable queue in the notifications flow points at. A direct
	// exchange is enough here — there's exactly one DLQ today, routed by
	// the exact routing key QueueNotificationsEmailDLQ.
	ExchangeNotificationsDLX = "notifications.dlx"

	// QueueNotificationsEmailDLQ holds messages dead-lettered off
	// QueueNotificationsEmail: unparseable payloads, or ones that fail
	// processing after notification-service's retry policy is exhausted
	// (see docs/DECISIONS.md — immediate nack-to-DLQ, no requeue, on this
	// queue specifically). Inspectable via the RabbitMQ management UI
	// (docker/docker-compose.infra.yml) or rabbitmqadmin, rather than
	// messages looping forever or vanishing.
	QueueNotificationsEmailDLQ = "notifications.email.dlq"

	// QueueNotificationsRealtime carries one RealtimeNotification JSON
	// payload per message (Phase 3.5) — published by auth-service's
	// Register and jobs-service's matching worker (on job.matched),
	// consumed only by api-gateway's realtime bridge, which republishes
	// each message onto Redis pub/sub for whichever gateway replica holds
	// that user's live onNotification subscription (see
	// internal/gateway/realtime and docs/DECISIONS.md's Phase 3.5 notes).
	//
	// Deliberately declared WITHOUT a dead-letter exchange, unlike
	// QueueNotificationsEmail above: this queue feeds a best-effort,
	// already-lossy UI ping (Redis pub/sub has no replay buffer, so even a
	// successfully delivered message here is silently dropped if no
	// gateway replica has a live subscriber listening at the moment it's
	// republished — see docs/DECISIONS.md). A DLQ exists to make a lost
	// message *recoverable*; there is nothing to recover a stale
	// real-time notification into days later, so the DLQ's own durability
	// buys nothing here that the email queue's DLQ genuinely buys for a
	// notification a human might actually want replayed. A malformed
	// message on this queue is logged at Error and acked (dropped), same
	// "log and move on, never crash the consumer" discipline as
	// everywhere else in this codebase — see
	// internal/gateway/realtime.Bridge.
	QueueNotificationsRealtime = "notifications.realtime"
)

// DeclareTopology declares ExchangeNotificationsDLX, QueueNotificationsEmailDLQ
// (bound to it), QueueNotificationsEmail (with dead-letter arguments
// pointing at both), and QueueNotificationsRealtime (Phase 3.5, no
// dead-letter arguments — see its doc comment above), on ch. Safe to call
// from every producer/consumer of this codebase's notifications topology
// at startup — the Go side (auth-service's and jobs-service's producers,
// api-gateway's realtime consumer) and the NestJS consumer side
// (notification-service, via its own equivalent declaration — see
// backend/notification-service/src/rabbitmq/rabbitmq.module.ts). AMQP
// declarations are idempotent as long as every caller passes identical
// arguments, which is why the exact table below must stay in sync with
// that TypeScript file if either ever changes.
func DeclareTopology(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(
		ExchangeNotificationsDLX, // name
		"direct",                 // kind
		true,                     // durable
		false,                    // auto-delete
		false,                    // internal
		false,                    // no-wait
		nil,                      // args
	); err != nil {
		return fmt.Errorf("rabbitmq: declare DLX exchange %q: %w", ExchangeNotificationsDLX, err)
	}

	if _, err := ch.QueueDeclare(
		QueueNotificationsEmailDLQ, // name
		true,                       // durable
		false,                      // auto-delete
		false,                      // exclusive
		false,                      // no-wait
		nil,                        // args — the DLQ itself has no further dead-lettering
	); err != nil {
		return fmt.Errorf("rabbitmq: declare DLQ %q: %w", QueueNotificationsEmailDLQ, err)
	}

	if err := ch.QueueBind(
		QueueNotificationsEmailDLQ, // queue
		QueueNotificationsEmailDLQ, // routing key — matches x-dead-letter-routing-key below
		ExchangeNotificationsDLX,   // exchange
		false,                      // no-wait
		nil,                        // args
	); err != nil {
		return fmt.Errorf("rabbitmq: bind DLQ %q to %q: %w", QueueNotificationsEmailDLQ, ExchangeNotificationsDLX, err)
	}

	if _, err := ch.QueueDeclare(
		QueueNotificationsEmail, // name
		true,                    // durable
		false,                   // auto-delete
		false,                   // exclusive
		false,                   // no-wait
		amqp.Table{
			"x-dead-letter-exchange":    ExchangeNotificationsDLX,
			"x-dead-letter-routing-key": QueueNotificationsEmailDLQ,
		},
	); err != nil {
		return fmt.Errorf("rabbitmq: declare main queue %q: %w", QueueNotificationsEmail, err)
	}

	if _, err := ch.QueueDeclare(
		QueueNotificationsRealtime, // name
		true,                       // durable
		false,                      // auto-delete
		false,                      // exclusive
		false,                      // no-wait
		nil,                        // args — deliberately no dead-lettering, see the const's doc comment
	); err != nil {
		return fmt.Errorf("rabbitmq: declare main queue %q: %w", QueueNotificationsRealtime, err)
	}

	return nil
}
