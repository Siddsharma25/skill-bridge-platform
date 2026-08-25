import type { Channel } from 'amqplib';

// Queue/exchange names — must match
// backend/internal/platform/rabbitmq/topology.go string-for-string. Kept
// as a separate exported const set (not re-derived from an env var or
// shared package, since Go and TypeScript can't share a source file
// directly) so a rename shows up as a compile-time-visible diff in two
// places instead of a silent runtime mismatch.
export const QUEUE_NOTIFICATIONS_EMAIL = 'notifications.email';
export const EXCHANGE_NOTIFICATIONS_DLX = 'notifications.dlx';
export const QUEUE_NOTIFICATIONS_EMAIL_DLQ = 'notifications.email.dlq';

/**
 * declareTopology declares the DLX exchange, the DLQ (bound to it), and
 * the main queue (with dead-letter arguments pointing at both) on ch —
 * the TypeScript mirror of
 * backend/internal/platform/rabbitmq/topology.go's DeclareTopology.
 *
 * Both this service and auth-service's Go producer declare the identical
 * topology at startup. That's deliberate, not redundant: AMQP's
 * queue.declare/exchange.declare are idempotent as long as every caller
 * passes the same arguments, and a RabbitMQ publish to the default
 * exchange with a routing key that doesn't match any existing queue is
 * silently dropped (not queued, not an error) — so relying on "whichever
 * service happens to start first declares it" would risk losing messages
 * published before notification-service's first startup in local dev,
 * where container start order isn't guaranteed. See
 * docs/DECISIONS.md's "Phase 3 implementation notes" for the fuller
 * reasoning.
 */
export async function declareTopology(ch: Channel): Promise<void> {
  await ch.assertExchange(EXCHANGE_NOTIFICATIONS_DLX, 'direct', {
    durable: true,
  });

  await ch.assertQueue(QUEUE_NOTIFICATIONS_EMAIL_DLQ, { durable: true });
  await ch.bindQueue(
    QUEUE_NOTIFICATIONS_EMAIL_DLQ,
    EXCHANGE_NOTIFICATIONS_DLX,
    QUEUE_NOTIFICATIONS_EMAIL_DLQ,
  );

  await ch.assertQueue(QUEUE_NOTIFICATIONS_EMAIL, {
    durable: true,
    arguments: {
      'x-dead-letter-exchange': EXCHANGE_NOTIFICATIONS_DLX,
      'x-dead-letter-routing-key': QUEUE_NOTIFICATIONS_EMAIL_DLQ,
    },
  });
}
