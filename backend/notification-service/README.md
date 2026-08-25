# notification-service

Consumes RabbitMQ `notifications.email` messages and logs/records a
"would send" welcome notification. This is the one non-Go service in the
platform (NestJS/TypeScript) — see the root `CLAUDE.md`'s "Don't wire real
email sending into notification-service" instruction and
`docs/DECISIONS.md` for the fuller reasoning behind every choice below.

## What it does

- Consumes `notifications.email` (published by auth-service's `Register`,
  see `backend/internal/platform/rabbitmq`), parses/validates each message
  as an `EmailNotification`, and for every valid one:
  - logs `would send welcome email to <email>` (no real email provider is
    integrated, by design — this line is the entire "send").
  - inserts a deduped row into `notifications.notification_log` via a
    plain `pg` `Pool` (see "Why plain `pg`" below).
- Malformed/unparseable messages are `nack`'d without requeue, which
  RabbitMQ routes to `notifications.email.dlq` via the queue's
  `x-dead-letter-exchange` argument (see "DLQ handling" below).
- Serves HTTP `/healthz` (liveness, no dependency checks) and `/readyz`
  (gated on the RabbitMQ connection; the database is reported but doesn't
  gate readiness — see `src/health/health.controller.ts`'s comment).

## Why NestJS + nestjs-pino + plain `pg`

This is the platform's one deliberate NestJS service (see
`docs/DECISIONS.md`'s service-split rationale) — genuine hands-on practice
with the Node/Nest side of the stack, not a Go rewrite in disguise.
`nestjs-pino` is the Node-side equivalent of every Go service's `zap`
logger: structured JSON output (not a pretty console format, even in local
dev — these logs are read by `docker logs`/grep, not a human staring at a
terminal), with `pino-pretty` layered on only when `ENV !== 'production'`
purely as a local convenience.

`pg` (not Prisma, not TypeORM) is deliberate: this service owns exactly
one table. Prisma's `migrate` wants a shadow database and doesn't play
cleanly with Supavisor's transaction-mode pooler without a separate
`directUrl`/`multiSchema` setup — a multi-hour trap for a single-table
service. A handwritten SQL migration
(`backend/migrations/notifications/00001_create_notification_log.sql`,
applied the same way as every Go service's via `goose` — see that file's
comment for why a Go-oriented migration CLI still fits a TypeScript
service fine) plus a couple of parameterized queries is simpler and more
honest about what's actually happening.

## Why `amqplib` directly, not `@nestjs/microservices`' RMQ transport

`@nestjs/microservices` is more idiomatic NestJS, but its RMQ strategy is
built around a message-handler-returns-response (or `Ctx`/`RmqContext`
with its own `channel.ack` helper wired through `enableManualAcknowledgment`)
shape that's a layer removed from raw `channel.ack`/`channel.nack`. This
service's whole point is precise control over one specific call:
`channel.nack(msg, false, false)` — requeue explicitly `false`, which is
what makes RabbitMQ apply the queue's `x-dead-letter-exchange` argument
and route the message to the DLQ, versus a default nack that would requeue
it forever. Wrapping `amqplib` directly in a small NestJS module
(`src/rabbitmq/rabbitmq-connection.service.ts` +
`src/rabbitmq/rabbitmq.consumer.ts`) keeps that one line unambiguous
rather than routed through a framework abstraction not designed around
this exact case. See `docs/DECISIONS.md`'s "Phase 3 implementation notes"
for the fuller comparison.

## DLQ handling

`src/rabbitmq/topology.ts` declares `notifications.dlx` (a direct
exchange), `notifications.email.dlq` (bound to it), and
`notifications.email` (with `x-dead-letter-exchange`/
`x-dead-letter-routing-key` arguments pointing at both) — the exact
TypeScript mirror of `backend/internal/platform/rabbitmq/topology.go`'s
`DeclareTopology`. **Both** auth-service's Go producer and this service's
consumer declare the identical topology at startup, rather than picking
one side as the sole "owner": AMQP's `queue.declare`/`exchange.declare`
are idempotent as long as every caller passes identical arguments, and a
RabbitMQ publish to a queue that doesn't exist yet is silently dropped
(not queued, not an error) — so relying on start order between the two
services would risk losing the very first registration's welcome
notification in local dev. See `docs/DECISIONS.md` for the fuller
reasoning.

Retry policy is zero retries: a malformed message can never succeed no
matter how many redeliveries, so `RabbitmqConsumer` routes it to the DLQ
immediately (`src/rabbitmq/rabbitmq.consumer.ts`). A message that's
well-formed but couldn't be fully processed because the database is down
is handled differently — logged and **acked**, not DLQ'd — since
Supabase's free-tier auto-pause is an expected reality for this project
(see `docs/DECISIONS.md`), not a poison message; see
`src/notifications/notifications.service.ts`'s `recordWelcomeEmail` doc
comment.

## Idempotency / dedupe

`notifications.notification_log.message_id` carries a `UNIQUE` constraint,
populated from `EmailNotification.message_id` — the idempotency key
auth-service's producer generates per notification (see
`backend/internal/platform/rabbitmq/events.go`). The insert is
`ON CONFLICT (message_id) DO NOTHING`, so redelivery of the exact same
AMQP message (a consumer restart before the original ack landed, a manual
republish while debugging) never produces a second row.

## Startup ordering (consumer attachment)

`RabbitmqConsumer.onModuleInit` needs `RabbitmqConnectionService.channel`
to already be live before it can call `channel.consume(...)`, but NestJS
runs every provider's `onModuleInit` within a module *concurrently* via
`Promise.all`, not sequenced by constructor-injection dependency order —
so a synchronous read of `channel` at the consumer's own hook-time would
frequently see `null` while the connection service's async
`amqp.connect()`/`createChannel()`/`declareTopology()` chain was still in
flight. Caught live during this phase's verification: the consumer's "not
configured" warning logged *before* the connection service's "connection
established" log, and the RabbitMQ management UI showed `0` consumers on
`notifications.email` despite `/readyz` already reporting connected.

Fix: `RabbitmqConnectionService` exposes `ready: Promise<void>`, resolved
in a `finally` block once its own `onModuleInit` has finished one way or
the other (channel live, or left `null`). `RabbitmqConsumer.onModuleInit`
awaits `ready` before reading `channel`, so it observes the *finished*
state of connection setup instead of racing it. See
`docs/DECISIONS.md`'s "Phase 3 implementation notes" for the live
re-verification (management API confirms `consumers: 1` on every
restart) and the mirrored shutdown-side race described below.

## Degraded-start behavior

Same philosophy as every Go service: a missing/unreachable `RABBITMQ_URL`
or `DATABASE_URL` never crashes the process at startup. Without RabbitMQ,
the consumer simply never starts (logged once) and `/readyz` reports
`not_ready`. Without the database, the consumer still runs and still logs
the "would send" line for every valid message — it just can't write
`notification_log` rows, logged at Error per message rather than treating
that message as failed. See `src/rabbitmq/rabbitmq-connection.service.ts`
and `src/db/pg.service.ts`.

## Graceful shutdown

`app.enableShutdownHooks()` in `src/main.ts` wires SIGINT/SIGTERM into
Nest's own module lifecycle: `RabbitmqConnectionService.onModuleDestroy`
runs `RabbitmqConsumer`'s registered cancel step first (see below), then
closes the channel and the connection, and `PgService.onModuleDestroy`
ends the pool — the NestJS-side equivalent of
`internal/platform/shutdown.Wait`'s sequence on the Go side.

`RabbitmqConsumer` doesn't implement `OnModuleDestroy` itself — NestJS
runs every provider's `onModuleDestroy` within a module *concurrently* via
`Promise.all`, the same non-dependency-order concurrency `ready` (see
"Startup ordering" above) already has to work around on startup. A plain
`OnModuleDestroy` on the
consumer would race `RabbitmqConnectionService` closing the channel out
from under `channel.cancel(consumerTag)` — caught live during this
phase's verification as a harmless-but-misleading
`IllegalOperationError: Channel closing` warning on an otherwise-clean
shutdown. Instead, `RabbitmqConsumer.onModuleInit` calls
`this.connection.registerBeforeClose(() => this.cancelConsumer())`, and
`RabbitmqConnectionService.onModuleDestroy` awaits that registered
callback before closing anything — deterministic ordering instead of
hoping `Promise.all` resolves them in a convenient order. See
`docs/DECISIONS.md`'s "Phase 3 implementation notes" for the fuller
writeup (this is the same race class as the consumer-attachment fix on
the startup side, mirrored on shutdown).

## Local run

```
cd backend/notification-service
npm install
npm run start:dev
# HTTP (health only) on :8085
curl localhost:8085/healthz
curl localhost:8085/readyz
```

## Tests

```
npm test       # unit tests: parsing, ack/nack decisions, notification_log dedupe logic
npm run test:e2e  # /healthz and /readyz over real HTTP, no live broker/DB required
```

No live RabbitMQ/Postgres is required for either — same "no broker
integration tests in CI" boundary the Go side draws (see
`docs/DECISIONS.md`'s Testing section). The actual DLQ round trip and the
cross-language RabbitMQ hop from auth-service are proven by this phase's
live verification, documented in `docs/DECISIONS.md`.
