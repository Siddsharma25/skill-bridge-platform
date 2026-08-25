import { Injectable } from '@nestjs/common';
import { InjectPinoLogger, PinoLogger } from 'nestjs-pino';
import { PgService } from '../db/pg.service';
import { EmailNotification } from '../rabbitmq/email-notification';

export type RecordResult = 'inserted' | 'duplicate' | 'db_unavailable';

/**
 * NotificationsService is the one place this service touches
 * notifications.notification_log. No real email provider is integrated —
 * "sending" is entirely the log line in recordWelcomeEmail, per this
 * project's documented "no real email provider" scope cut (see
 * docs/DECISIONS.md and root CLAUDE.md's "Don't wire real email sending
 * into notification-service" instruction).
 */
@Injectable()
export class NotificationsService {
  constructor(
    private readonly pg: PgService,
    @InjectPinoLogger(NotificationsService.name)
    private readonly logger: PinoLogger,
  ) {}

  /**
   * recordWelcomeEmail logs the "would send" line (the only observable
   * effect of "sending" in this project) and, if the database is
   * configured and reachable, inserts a deduped row into
   * notification_log.
   *
   * Dedup is `ON CONFLICT (message_id) DO NOTHING` — message_id is the
   * idempotency key auth-service's producer generates per notification
   * (see backend/internal/platform/rabbitmq/events.go), so redelivery of
   * the exact same AMQP message (a consumer restart before the original
   * ack landed, or a manual republish while debugging) never produces a
   * second row. This is the "idempotency key from the message" option
   * documented in docs/DECISIONS.md, chosen over a unique constraint on a
   * natural key like (user_id, event_type) because a future event type
   * (e.g. a "job matched" notification, later phase) could legitimately
   * fire more than once for the same user.
   *
   * Returns 'db_unavailable' (not a thrown error) when the database isn't
   * configured/reachable — RabbitmqConsumer acks the message either way
   * in that case rather than routing it to the DLQ, since the message
   * itself is perfectly valid and a transient/unconfigured database
   * (Supabase's free-tier auto-pause is an expected reality for this
   * project, not an edge case) shouldn't be treated the same as a poison
   * message that can never succeed. See
   * docs/DECISIONS.md's "Phase 3 implementation notes" for this
   * distinction.
   */
  async recordWelcomeEmail(payload: EmailNotification): Promise<RecordResult> {
    this.logger.info(
      {
        userId: payload.user_id,
        email: payload.email,
        eventType: payload.event_type,
      },
      `would send welcome email to ${payload.email}`,
    );

    if (!this.pg.enabled || !this.pg.pool) {
      this.logger.error(
        { userId: payload.user_id, messageId: payload.message_id },
        'database not configured; skipping notification_log write',
      );
      return 'db_unavailable';
    }

    try {
      const result = await this.pg.pool.query(
        `INSERT INTO notifications.notification_log (message_id, user_id, email, event_type)
         VALUES ($1, $2, $3, $4)
         ON CONFLICT (message_id) DO NOTHING
         RETURNING id`,
        [
          payload.message_id,
          payload.user_id,
          payload.email,
          payload.event_type,
        ],
      );

      if (result.rowCount === 0) {
        this.logger.info(
          { messageId: payload.message_id },
          'notification_log row already exists for this message_id; skipped duplicate insert',
        );
        return 'duplicate';
      }
      return 'inserted';
    } catch (err: unknown) {
      // A query-time failure (e.g. the pool started healthy but the
      // connection dropped mid-flight) is treated the same as
      // "unconfigured" — see this method's doc comment for why that's
      // deliberate rather than routing to the DLQ.
      this.logger.error(
        { err, userId: payload.user_id, messageId: payload.message_id },
        'failed to write notification_log row',
      );
      return 'db_unavailable';
    }
  }
}
