import { Controller, Get, HttpStatus, Res } from '@nestjs/common';
import type { Response } from 'express';
import { PgService } from '../db/pg.service';
import { RabbitmqConnectionService } from '../rabbitmq/rabbitmq-connection.service';

/**
 * HealthController mirrors every Go service's /healthz + /readyz pair
 * (see backend/internal/platform/health) for consistency across the
 * whole platform — this service has no gRPC/GraphQL surface of its own,
 * but Docker/Render/k8s (later phases) still expect a health endpoint on
 * every service, so it's added now rather than retrofitted when those
 * phases actually need it.
 *
 * Same liveness/readiness split as the Go side: /healthz never checks a
 * dependency (a slow/down broker or database shouldn't get a healthy
 * process killed and restarted), /readyz reports whether this instance
 * can currently do useful work (RabbitMQ connected; database is
 * best-effort so its absence doesn't flip readiness — see below).
 */
@Controller()
export class HealthController {
  constructor(
    private readonly pg: PgService,
    private readonly rabbitmq: RabbitmqConnectionService,
  ) {}

  @Get('healthz')
  healthz(): { status: string } {
    return { status: 'ok' };
  }

  @Get('readyz')
  readyz(@Res() res: Response): void {
    // Database is reported but deliberately does NOT flip /readyz to
    // "not ready" on its own — notifications are already documented as
    // best-effort (see NotificationsService.recordWelcomeEmail), and
    // Supabase's free-tier auto-pause is an expected, non-exceptional
    // state for this project (see docs/DECISIONS.md), not a reason to
    // pull this instance out of rotation. RabbitMQ being down, by
    // contrast, means this instance is doing nothing at all (no consumer
    // running), so it does gate readiness.
    const rabbitmqStatus = this.rabbitmq.enabled
      ? 'connected'
      : 'not_configured';
    const databaseStatus = this.pg.enabled ? 'connected' : 'not_configured';
    const ready = this.rabbitmq.enabled;
    res.status(ready ? HttpStatus.OK : HttpStatus.SERVICE_UNAVAILABLE).json({
      status: ready ? 'ok' : 'not_ready',
      rabbitmq: rabbitmqStatus,
      database: databaseStatus,
    });
  }
}
