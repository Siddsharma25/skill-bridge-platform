import { Module } from '@nestjs/common';
import { LoggerModule } from 'nestjs-pino';
import { randomUUID } from 'node:crypto';
import { HealthModule } from './health/health.module';
import { RabbitmqModule } from './rabbitmq/rabbitmq.module';
import { NotificationsModule } from './notifications/notifications.module';
import { envOr } from './config/env';

@Module({
  imports: [
    // Structured JSON logging via nestjs-pino — the Node-side equivalent
    // of every Go service's zap logger (see
    // backend/internal/platform/logger and docs/DECISIONS.md): JSON, not
    // a human-readable pretty-printer, even for local dev, since these
    // logs are read by `docker logs`/grep/later log aggregation, not a
    // human staring at a terminal. `pino-pretty` is still wired in below,
    // gated on ENV !== 'production', purely as a local-dev convenience —
    // same "dev convenience, not the default shape" reasoning godotenv
    // gets on the Go side.
    LoggerModule.forRoot({
      pinoHttp: {
        // x-request-id propagation (see docs/DECISIONS.md's Correlation
        // IDs note and backend/internal/platform/requestid on the Go
        // side): this service has no gRPC/GraphQL calls of its own to
        // propagate a header through, but every HTTP request (health
        // checks today) still gets a request-scoped id in its log lines,
        // consistent with the rest of the platform.
        genReqId: (req) => {
          const header = req.headers['x-request-id'];
          return (Array.isArray(header) ? header[0] : header) ?? randomUUID();
        },
        base: { service: 'notification-service' },
        level: envOr('LOG_LEVEL', 'info'),
        transport:
          envOr('ENV', 'development') === 'production'
            ? undefined
            : { target: 'pino-pretty', options: { singleLine: true } },
        // Health-check polling shouldn't spam the log at info level.
        autoLogging: {
          ignore: (req) => req.url === '/healthz' || req.url === '/readyz',
        },
      },
    }),
    HealthModule,
    RabbitmqModule,
    NotificationsModule,
  ],
})
export class AppModule {}
