import { Injectable, OnModuleDestroy, OnModuleInit } from '@nestjs/common';
import { InjectPinoLogger, PinoLogger } from 'nestjs-pino';
import * as amqp from 'amqplib';
import { env } from '../config/env';
import { declareTopology } from './topology';

/**
 * RabbitmqConnectionService owns the one AMQP connection/channel this
 * service uses, following the same degrade-gracefully shape as every
 * external dependency elsewhere in this codebase (see
 * backend/internal/platform/kafka and .../rabbitmq on the Go side): a
 * missing or unreachable RABBITMQ_URL logs a warning/error and leaves
 * `channel` null rather than crashing the process at startup — the
 * service still boots and serves /healthz, it just doesn't consume
 * anything until RabbitMQ is reachable and the process is restarted (no
 * reconnect-loop is implemented; see docs/DECISIONS.md for why that's an
 * accepted gap for a learning project's local-dev broker, same as the
 * rest of this codebase not implementing broker HA/reconnect anywhere).
 *
 * Chose @nestjs' plain OnModuleInit/OnModuleDestroy lifecycle hooks over
 * @nestjs/microservices' RMQ transport specifically so RabbitmqConsumer
 * (see rabbitmq.consumer.ts) can call channel.ack/channel.nack directly —
 * @nestjs/microservices' RMQ strategy wants noAck or its own
 * ackAll/handled-message helpers, which don't cleanly expose the
 * per-message "nack without requeue, so it dead-letters" control this
 * service's DLQ behavior depends on. See
 * backend/notification-service/README.md and docs/DECISIONS.md for the
 * fuller amqplib-vs-@nestjs/microservices comparison.
 */
@Injectable()
export class RabbitmqConnectionService
  implements OnModuleInit, OnModuleDestroy
{
  private connection: amqp.ChannelModel | null = null;
  channel: amqp.Channel | null = null;

  // Resolves once connection setup has finished, one way or the other
  // (channel live, or left null after a missing/unreachable
  // RABBITMQ_URL). NestJS calls every provider's onModuleInit within a
  // module concurrently via Promise.all (see
  // @nestjs/core/hooks/on-module-init.hook.js's callOperator) rather than
  // sequencing them by constructor-injection dependency order — so
  // RabbitmqConsumer.onModuleInit (see rabbitmq.consumer.ts) cannot just
  // read `channel` synchronously at its own hook-time, since this
  // service's async amqp.connect()/createChannel()/declareTopology() call
  // chain is frequently still in flight when the consumer's hook runs
  // (confirmed live: the consumer's "not configured" warning logged
  // *before* this service's "connection established" log, and the
  // RabbitMQ management UI showed 0 consumers on notifications.email
  // despite /readyz reporting "connected" — the consumer's synchronous
  // check had already run and given up by the time the channel existed).
  // Awaiting `ready` first is what makes RabbitmqConsumer.onModuleInit
  // observe the *finished* state of this hook rather than racing it.
  readonly ready: Promise<void>;
  private resolveReady!: () => void;

  // Same concurrency hazard as `ready` above, mirrored on shutdown:
  // NestJS calls every provider's onModuleDestroy within a module via
  // Promise.all too (see @nestjs/core/hooks/on-module-destroy.hook.js's
  // callOperator — identical shape to on-module-init.hook.js), not in
  // dependency order and not in reverse-of-init order either. Without
  // this, RabbitmqConsumer.onModuleDestroy's channel.cancel(consumerTag)
  // races this service's own onModuleDestroy closing the channel out from
  // under it — confirmed live: a clean SIGINT logged "error cancelling
  // rabbitmq consumer during shutdown" with an amqplib
  // `IllegalOperationError: Channel closing`, harmless (the channel close
  // already implicitly cancels any consumer on it, so nothing actually
  // leaked) but a misleading warning on every graceful shutdown.
  // beforeClose lets RabbitmqConsumer register its own cleanup to run
  // *before* this service closes the channel/connection, deterministically,
  // instead of hoping Promise.all resolves them in a convenient order.
  private beforeClose: (() => Promise<void>) | null = null;

  constructor(
    @InjectPinoLogger(RabbitmqConnectionService.name)
    private readonly logger: PinoLogger,
  ) {
    this.ready = new Promise<void>((resolve) => {
      this.resolveReady = resolve;
    });
  }

  /**
   * registerBeforeClose lets a dependent (RabbitmqConsumer) run its own
   * shutdown step — cancelling its consumer tag — before this service
   * closes the channel/connection, avoiding the onModuleDestroy race
   * described above. Only one registrant is expected today (there's only
   * one consumer); a second call simply replaces the first.
   */
  registerBeforeClose(fn: () => Promise<void>): void {
    this.beforeClose = fn;
  }

  get enabled(): boolean {
    return this.channel !== null;
  }

  async onModuleInit(): Promise<void> {
    try {
      const url = env('RABBITMQ_URL');
      if (!url) {
        this.logger.warn(
          'RABBITMQ_URL not set; notifications.email consumer will not start on this instance',
        );
        return;
      }

      try {
        this.connection = await amqp.connect(url);
        this.connection.on('error', (err: Error) => {
          this.logger.error({ err }, 'rabbitmq connection error');
        });

        this.channel = await this.connection.createChannel();
        this.channel.on('error', (err: Error) => {
          this.logger.error({ err }, 'rabbitmq channel error');
        });

        await declareTopology(this.channel);
        this.logger.info(
          'rabbitmq connection established and topology declared',
        );
      } catch (err: unknown) {
        this.logger.error(
          { err },
          'failed to connect to rabbitmq; notifications.email consumer will not start on this instance',
        );
        this.channel = null;
        this.connection = null;
      }
    } finally {
      this.resolveReady();
    }
  }

  async onModuleDestroy(): Promise<void> {
    // Run the registered beforeClose step (RabbitmqConsumer cancelling its
    // consumer tag) first and deterministically — see beforeClose's doc
    // comment for why this can't just be "declare RabbitmqConsumer first
    // and hope" the way onModuleInit's `ready` await already had to solve
    // on the startup side.
    if (this.beforeClose) {
      try {
        await this.beforeClose();
      } catch (err: unknown) {
        this.logger.warn({ err }, 'error running rabbitmq beforeClose hook');
      }
    }

    // Order matters: close the channel (which cancels any remaining
    // consumer) before the connection, mirroring the Go producer's
    // Close() — see internal/platform/rabbitmq/producer.go.
    try {
      await this.channel?.close();
    } catch (err: unknown) {
      this.logger.warn(
        { err },
        'error closing rabbitmq channel during shutdown',
      );
    }
    try {
      await this.connection?.close();
    } catch (err: unknown) {
      this.logger.warn(
        { err },
        'error closing rabbitmq connection during shutdown',
      );
    }
  }
}
