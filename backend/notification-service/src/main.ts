import { NestFactory } from '@nestjs/core';
import { Logger } from 'nestjs-pino';
import { AppModule } from './app.module';
import { envOr } from './config/env';

async function bootstrap() {
  const env = process.env.ENV;
  if (env !== 'production') {
    // Local dev convenience only, same gating as godotenv.Load() in every
    // Go cmd/<service>/main.go — ignored if backend/notification-service/.env
    // doesn't exist, which is expected in CI and in production.
    await import('dotenv').then((dotenv) => dotenv.config());
  }

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
