import { Injectable, OnModuleDestroy, OnModuleInit } from '@nestjs/common';
import { InjectPinoLogger, PinoLogger } from 'nestjs-pino';
import { Pool } from 'pg';
import { env } from '../config/env';

/**
 * PgService wraps a plain `pg` Pool against the `notifications` schema —
 * deliberately not Prisma/TypeORM (see docs/DECISIONS.md's
 * "notification-service's ORM" note: this service owns exactly one
 * table, and Prisma's shadow-database migrate story doesn't play nicely
 * with Supavisor's transaction-mode pooler for no pedagogical gain here).
 *
 * Same degrade-gracefully pattern as every other service's DB connection
 * (see backend/internal/platform/db on the Go side, and
 * backend/cmd/auth-service/main.go's comment on why): an unset or
 * unreachable DATABASE_URL leaves `pool` null rather than crashing the
 * process at startup, and every query call site checks `enabled` first.
 */
@Injectable()
export class PgService implements OnModuleInit, OnModuleDestroy {
  pool: Pool | null = null;

  constructor(
    @InjectPinoLogger(PgService.name)
    private readonly logger: PinoLogger,
  ) {}

  get enabled(): boolean {
    return this.pool !== null;
  }

  async onModuleInit(): Promise<void> {
    const url = env('DATABASE_URL');
    if (!url) {
      this.logger.warn(
        'DATABASE_URL not set; notification_log writes are disabled on this instance',
      );
      return;
    }

    // MaxOpenConns-equivalent kept small (5) for the same reason every Go
    // service's db.Config does — see backend/internal/platform/db and
    // docs/DECISIONS.md: Supavisor's transaction-mode pooler budget is
    // shared across every running service.
    const pool = new Pool({ connectionString: url, max: 5 });
    pool.on('error', (err) => {
      // A pool-level error fires on an idle client (e.g. the connection
      // was dropped server-side) — logging it here is what stops that
      // from becoming an unhandled 'error' event that crashes the
      // process, which node's EventEmitter does by default when nothing
      // is listening.
      this.logger.error({ err }, 'postgres pool error');
    });

    try {
      await pool.query('SELECT 1');
    } catch (err: unknown) {
      this.logger.error(
        { err },
        'failed to reach postgres; notification_log writes are disabled on this instance',
      );
      await pool.end().catch(() => undefined);
      return;
    }

    this.pool = pool;
    this.logger.info('connected to database');
  }

  async onModuleDestroy(): Promise<void> {
    if (!this.pool) {
      return;
    }
    try {
      await this.pool.end();
    } catch (err: unknown) {
      this.logger.warn({ err }, 'error closing postgres pool during shutdown');
    }
  }
}
