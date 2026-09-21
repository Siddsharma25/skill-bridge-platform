import { Injectable, OnModuleInit } from '@nestjs/common';
import { InjectPinoLogger, PinoLogger } from 'nestjs-pino';
import * as Sentry from '@sentry/node';
import type { ConsumeMessage } from 'amqplib';
import { RabbitmqConnectionService } from './rabbitmq-connection.service';
import { NotificationsService } from '../notifications/notifications.service';
import { QUEUE_NOTIFICATIONS_EMAIL } from './topology';
import {
  MalformedMessageError,
  parseEmailNotification,
} from './email-notification';

/**
 * RabbitmqConsumer subscribes to QUEUE_NOTIFICATIONS_EMAIL and drives
 * every message to exactly one outcome: ack (successfully processed, or
 * a valid message we couldn't fully process because the database is
 * down — see NotificationsService.recordWelcomeEmail's doc comment), or
 * nack-without-requeue (a malformed/unparseable message, which the
 * queue's x-dead-letter-exchange argument routes straight to
 * notifications.email.dlq — see topology.ts).
 *
 * Retry policy: zero retries. A malformed message can never succeed no
 * matter how many times it's redelivered, so there's nothing a retry
 * would accomplish beyond delaying the inevitable DLQ trip — see
 * docs/DECISIONS.md's "Phase 3 implementation notes" for the fuller
 * "why not requeue-with-backoff" reasoning (short version: this is a
 * learning project's local broker, not a production SLA, and a second
 * retry queue is real complexity for a case documented in the Scope Cuts
 * as an at-least-once/idempotent-consumer world already).
 *
 * Both real failure paths (a malformed message, an unexpected processing
 * error) also report to Sentry, not just the pino log line — see main.ts
 * and docs/DECISIONS.md's Sentry section for why: this service's only
 * HTTP surface is health checks, so nothing else here would ever surface
 * a bug in this service's own code.
 */
@Injectable()
export class RabbitmqConsumer implements OnModuleInit {
  private consumerTag: string | null = null;

  constructor(
    private readonly connection: RabbitmqConnectionService,
    private readonly notifications: NotificationsService,
    @InjectPinoLogger(RabbitmqConsumer.name)
    private readonly logger: PinoLogger,
  ) {}

  async onModuleInit(): Promise<void> {
    // Must wait for RabbitmqConnectionService's own onModuleInit to
    // actually finish before reading `channel` — see `ready`'s doc
    // comment on RabbitmqConnectionService for why a synchronous read
    // here loses a race against that service's async connection setup
    // (NestJS runs both hooks concurrently, not in dependency order).
    await this.connection.ready;
    const channel = this.connection.channel;
    if (!channel) {
      this.logger.warn(
        `rabbitmq not configured; ${QUEUE_NOTIFICATIONS_EMAIL} consumer will not start`,
      );
      return;
    }

    // One unacked message in flight per consumer at a time — this
    // service does nothing CPU/IO-heavy per message, and a small prefetch
    // keeps behavior easy to reason about for a learning project rather
    // than tuning for throughput.
    await channel.prefetch(10);

    const { consumerTag } = await channel.consume(
      QUEUE_NOTIFICATIONS_EMAIL,
      (msg) => {
        // consume()'s callback isn't awaited by amqplib itself, so a
        // rejected promise here would become an unhandled rejection —
        // handleMessage catches everything it can, but this .catch is
        // the last line of defense that keeps a truly unexpected bug from
        // crashing the whole process (this is the specific "consumer
        // doesn't crash/hang" guarantee the phase's live DLQ proof
        // depends on).
        void this.handleMessage(msg).catch((err: unknown) => {
          this.logger.error(
            { err },
            'unexpected error handling notifications.email message; message left unacked',
          );
          // This service's HTTP surface is health-checks only (see
          // main.ts), so there's no request-layer Sentry instrumentation
          // that would ever see this — this is the one place this
          // service's own bugs would otherwise be silent beyond the log
          // line above. No-op if SENTRY_DSN isn't set (see main.ts).
          Sentry.captureException(err);
        });
      },
      { noAck: false },
    );
    this.consumerTag = consumerTag;
    this.logger.info({ consumerTag }, `consuming ${QUEUE_NOTIFICATIONS_EMAIL}`);

    // Register our own cancel step to run *before*
    // RabbitmqConnectionService closes the channel/connection on shutdown
    // — see RabbitmqConnectionService.beforeClose's doc comment for why a
    // plain `implements OnModuleDestroy` here would race that service's
    // own onModuleDestroy (both run concurrently via NestJS's
    // Promise.all, the exact same hazard `ready` above already works
    // around for startup).
    this.connection.registerBeforeClose(() => this.cancelConsumer());
  }

  private async cancelConsumer(): Promise<void> {
    const channel = this.connection.channel;
    if (channel && this.consumerTag) {
      try {
        await channel.cancel(this.consumerTag);
      } catch (err: unknown) {
        this.logger.warn(
          { err },
          'error cancelling rabbitmq consumer during shutdown',
        );
      }
    }
  }

  private async handleMessage(msg: ConsumeMessage | null): Promise<void> {
    if (!msg) {
      // amqplib passes null if the consumer was server-side cancelled
      // (e.g. the queue was deleted) — nothing to ack/nack.
      return;
    }
    const channel = this.connection.channel;
    if (!channel) {
      return;
    }

    let payload;
    try {
      payload = parseEmailNotification(msg.content);
    } catch (err: unknown) {
      if (err instanceof MalformedMessageError) {
        this.logger.error(
          { err: err.message, raw: msg.content.toString('utf8').slice(0, 500) },
          `malformed ${QUEUE_NOTIFICATIONS_EMAIL} message; routing to DLQ`,
        );
        // A malformed message means some publisher upstream is producing
        // bad payloads — worth surfacing on its own, not just visible as
        // a growing DLQ depth someone has to think to go check.
        Sentry.captureException(err);
        channel.nack(msg, false, false);
        return;
      }
      throw err;
    }

    const result = await this.notifications.recordWelcomeEmail(payload);
    this.logger.info(
      { userId: payload.user_id, messageId: payload.message_id, result },
      'processed notifications.email message',
    );
    // Acked in every non-malformed outcome, including 'db_unavailable' —
    // see NotificationsService.recordWelcomeEmail's doc comment for why a
    // valid message isn't punished with a DLQ trip just because the
    // database happens to be down right now.
    channel.ack(msg);
  }
}
