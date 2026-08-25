import { Test, TestingModule } from '@nestjs/testing';
import { INestApplication } from '@nestjs/common';
import { LoggerModule } from 'nestjs-pino';
import request from 'supertest';
import type { App } from 'supertest/types';
import { HealthModule } from '../src/health/health.module';

// A lean e2e slice: the full AppModule needs a real RabbitMQ/Postgres to
// connect (or, per the degrade-gracefully design, cleanly not connect —
// either way it'd depend on ambient env vars this test shouldn't rely
// on). LoggerModule + HealthModule alone are already what /healthz and
// /readyz need, and this proves the HTTP surface actually boots and
// responds, same "no live broker/DB required for `npm test`" boundary as
// the Go side's `go test` (see docs/DECISIONS.md's Testing section and
// internal/platform/kafka's tests for the equivalent reasoning there).
describe('Health (e2e)', () => {
  let app: INestApplication<App>;

  beforeEach(async () => {
    const moduleFixture: TestingModule = await Test.createTestingModule({
      imports: [
        LoggerModule.forRoot({ pinoHttp: { level: 'silent' } }),
        HealthModule,
      ],
    }).compile();

    app = moduleFixture.createNestApplication();
    await app.init();
  });

  it('/healthz (GET) is always ok, no dependency checks', async () => {
    const res = await request(app.getHttpServer()).get('/healthz').expect(200);
    const body = res.body as { status: string };
    expect(body.status).toBe('ok');
  });

  it('/readyz (GET) reports not_ready when RabbitMQ/Postgres are unconfigured', async () => {
    const res = await request(app.getHttpServer()).get('/readyz').expect(503);
    const body = res.body as {
      status: string;
      rabbitmq: string;
      database: string;
    };
    expect(body.status).toBe('not_ready');
    expect(body.rabbitmq).toBe('not_configured');
    expect(body.database).toBe('not_configured');
  });

  afterEach(async () => {
    await app.close();
  });
});
