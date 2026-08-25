// EmailNotification is QUEUE_NOTIFICATIONS_EMAIL's payload shape — the
// TypeScript mirror of backend/internal/platform/rabbitmq/events.go's
// EmailNotification struct. Field names match exactly (snake_case, as
// produced by Go's encoding/json with these json tags) since this is a
// plain JSON envelope crossing a language boundary, not a shared type.
export interface EmailNotification {
  message_id: string;
  user_id: string;
  email: string;
  event_type: string;
  created_at: string;
}

/**
 * MalformedMessageError marks a message as unrecoverable — not just a
 * JSON parse failure, but any structurally invalid payload (missing/
 * empty required field). RabbitmqConsumer treats this specifically as
 * "never retry, route to the DLQ immediately" (see that file), as
 * opposed to a downstream processing failure (e.g. the database being
 * unreachable) which is handled differently — see
 * docs/DECISIONS.md's "Phase 3 implementation notes" for why the two
 * are NOT treated the same way here.
 */
export class MalformedMessageError extends Error {}

const REQUIRED_STRING_FIELDS: (keyof EmailNotification)[] = [
  'message_id',
  'user_id',
  'email',
  'event_type',
  'created_at',
];

/**
 * parseEmailNotification parses and validates a raw AMQP message body as
 * an EmailNotification, throwing MalformedMessageError for anything that
 * isn't valid JSON, isn't a JSON object, or is missing/blank a required
 * field. Deliberately strict (rather than defaulting missing fields) —
 * every field here is either the idempotency key (message_id) or
 * something notification_log's NOT NULL columns require, so a payload
 * missing one of them can never be processed successfully no matter how
 * many times it's redelivered.
 */
export function parseEmailNotification(raw: Buffer): EmailNotification {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw.toString('utf8'));
  } catch (err: unknown) {
    throw new MalformedMessageError(
      `invalid JSON: ${err instanceof Error ? err.message : String(err)}`,
    );
  }

  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    throw new MalformedMessageError('payload is not a JSON object');
  }

  const record = parsed as Record<string, unknown>;
  for (const field of REQUIRED_STRING_FIELDS) {
    const value = record[field];
    if (typeof value !== 'string' || value.trim() === '') {
      throw new MalformedMessageError(
        `missing or empty required field "${field}"`,
      );
    }
  }

  return record as unknown as EmailNotification;
}
