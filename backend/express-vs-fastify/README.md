# express-vs-fastify

A standalone, isolated comparison — **not used by any other part of this project**. `notification-service` still runs on NestJS's default Express adapter, untouched; this exists purely to see Fastify's actual behavior side by side with Express on the identical route, since the two are genuinely different libraries at a level NestJS's abstraction otherwise hides (see the root conversation/`docs/DECISIONS.md` for why a NestJS adapter swap wasn't done instead).

## What's here

`express-server.js` and `fastify-server.js` each implement the exact same two routes:

- `GET /health` → `{ "status": "ok" }`
- `POST /echo` — validates `{ message: string (non-empty), priority: integer 1-5 }`, echoes it back with a `receivedAt` timestamp

**Express's validation is hand-written** (manual `typeof`/range checks, the normal pattern without reaching for a separate library like `ajv`/`joi`/`zod`). **Fastify's is a declared JSON Schema** on the route — Fastify validates the request against it automatically (returning a structured `400` before the handler ever runs) and uses the *response* schema to pre-compile a fast serializer, which is the concrete mechanism behind its serialization speed, not just "a leaner framework."

Confirmed live — the same invalid request (`priority: 99`) against both:

```
express: {"error":"priority must be an integer between 1 and 5"}
fastify: {"statusCode":400,"code":"FST_ERR_VALIDATION","error":"Bad Request","message":"body/priority must be <= 5"}
```

## Running it

```bash
npm install
PORT=4101 npm run express   # in one terminal
PORT=4102 npm run fastify   # in another
```

(Ports are only env-overridable to sidestep whatever else might already be running on 3000/3001 locally — there's nothing special about 4101/4102.)

## Benchmark — real numbers, not claimed ones

```bash
EXPRESS_URL=http://localhost:4101/echo FASTIFY_URL=http://localhost:4102/echo npm run bench
```

Actual result from this machine (50 connections, 10s, identical `POST /echo` body):

| | requests/sec | avg latency | throughput |
|---|---|---|---|
| Express | 12,107 | 3.75 ms | 3.80 MB/s |
| Fastify | 35,812 | 1.13 ms | 9.05 MB/s |

Fastify: **~3x the throughput, ~3.3x lower latency**, on the identical route, on the identical machine, at the identical moment. Re-run `npm run bench` yourself if you want fresh numbers — they'll vary run to run, but the ratio holds consistently for this same-work comparison.

## Why this stays separate from notification-service

`notification-service`'s actual job is consuming RabbitMQ messages, not serving high-throughput HTTP — the performance difference demonstrated here is close to irrelevant to what that service does. This directory exists purely so the difference between the two libraries is visible and runnable, without conflating it with a real (and unnecessary) change to a service that's working fine as-is.
