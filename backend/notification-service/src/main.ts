import { NestFactory } from '@nestjs/core';
import { Logger } from 'nestjs-pino';
import * as Sentry from '@sentry/node';
import { AppModule } from './app.module';
import { env, envOr } from './config/env';

// Sentry (error tracking + log capture) — this service's half of this
// repo's one Sentry integration; see backend/internal/platform/sentry (the
// Go side) and docs/DECISIONS.md's Sentry section. Called before
// NestFactory.create so a crash during module construction itself is
// still reportable, not just errors inside a running request/consumer.
// Same degrade-gracefully convention as every other optional dependency
// in this service (see rabbitmq-connection.service.ts): no SENTRY_DSN
// means this is a no-op, not a startup failure.
//
// Only local/kind value today, not production — this service isn't part
// of render.yaml's deploy (see cmd/allinone/main.go's package doc comment:
// notification-service is deliberately excluded from production). Wired
// in anyway per this repo's stated rule that internal/platform-equivalent
// behavior isn't optional per-service boilerplate to skip.
function initSentry(): void {
  const dsn = env('SENTRY_DSN');
  if (!dsn) {
    return;
  }
  Sentry.init({
    dsn,
    environment: envOr('SENTRY_ENVIRONMENT', 'development'),
    serverName: 'notification-service',
    tracesSampleRate: Number(envOr('SENTRY_TRACES_SAMPLE_RATE', '0')) || 0,
  });
}

async function bootstrap() {
  const env = process.env.ENV;
  if (env !== 'production') {
    // Local dev convenience only, same gating as godotenv.Load() in every
    // Go cmd/<service>/main.go — ignored if backend/notification-service/.env
    // doesn't exist, which is expected in CI and in production.
    await import('dotenv').then((dotenv) => dotenv.config());
  }

  initSentry();

  const app = await NestFactory.create(AppModule, { bufferLogs: true });
  app.useLogger(app.get(Logger));

  // enableShutdownHooks wires SIGINT/SIGTERM to Nest's own lifecycle:
  // every module's onModuleDestroy runs (RabbitmqConnectionService closes
  // its channel/connection, PgService ends its pool, RabbitmqConsumer
  // cancels its consumer tag) before the process exits — the NestJS-side
  // equivalent of backend/internal/platform/shutdown.Wait's sequence on
  // the Go side. Without this, a local Ctrl-C would drop the AMQP
  // connection uncleanly instead of RabbitMQ seeing a clean channel
  // close/consumer cancel.
  app.enableShutdownHooks();

  const port = envOr('HTTP_PORT', '8085');
  await app.listen(port);
  app
    .get(Logger)
    .log(`notification-service listening on port ${port} (health checks only)`);
}

void bootstrap();
