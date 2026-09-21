# backend

This is the Go half of skill-bridge-platform: a set of gRPC microservices
fronted by a GraphQL gateway. It's a **learning project** — see the root
[`docs/DECISIONS.md`](../docs/DECISIONS.md) for the full architecture and
the reasoning behind every non-obvious choice before making structural
changes here.

## What's here (Phase 1b)

- **`cmd/auth-service`** — owns password credentials and JWT issuance.
  Speaks gRPC only (private network); see its own README for why.
- **`cmd/skills-service`** — owns the shared skill taxonomy.
- **`cmd/users-service`** — owns profile data and a user's claimed skills;
  lazily provisions a profile on first touch (see `docs/DECISIONS.md`).
- **`cmd/jobs-service`** — owns job postings and their required skills.
- **`cmd/api-gateway`** — the public GraphQL edge. Holds no state of its
  own; every resolver is a thin pass-through to a backend gRPC service.
  `myProfile`/`updateProfile`/`addUserSkill` are the first authenticated
  operations — see `internal/gateway/authctx` and `docs/DECISIONS.md`.
- **`cmd/allinone`** — Phase 7's production deploy target: one process
  registering all four backend services' gRPC servers on loopback plus
  api-gateway's public HTTP/GraphQL/WebSocket server, assembled from the
  exact same `NewServer`/resolver-wiring code as the five services above.
  See `cmd/allinone/README.md` and `docs/DECISIONS.md`'s Phase 7 notes for
  why (short version: Render's free tier is realistically one web
  service). Not part of local dev's everyday workflow — `go run
  ./cmd/<service>` against `make up`'s infra-only stack, per below, still
  is.
- **`internal/platform`** — the shared foundation every service uses:
  logging, request-ID propagation, health checks, graceful shutdown, the
  Postgres connection helper, JWKS signing/verification, Redis caching,
  Kafka/RabbitMQ clients, and Sentry error tracking (added across later
  phases — see its own README for the full list). Built once in Phase 1a,
  extended, not reimplemented per-service, as later phases needed more.
- **`proto/`** — gRPC contracts, proto-first via `buf`. See `proto/README.md`.
- **`gen/`** — generated Go stubs from `proto/`. **Committed to git**, not
  regenerated as a hidden prerequisite — a clean checkout builds without
  running codegen first.
- **`migrations/`** — goose SQL migrations, one directory per service
  schema, plus `000_bootstrap.sql` (schemas + per-service Postgres roles,
  run once with an admin connection).

Later phases add Kafka, RabbitMQ, Redis caching, GraphQL dataloaders,
Google OAuth, and a NestJS `notification-service` — see the Phased Rollout
in the architecture plan. Don't build ahead of the current phase; each
phase is independently demoable on purpose.

## Why gRPC + GraphQL, not just REST

The gateway is the only thing the frontend ever talks to, over GraphQL —
one flexible query language instead of four services' worth of bespoke REST
shapes. Behind the gateway, services talk gRPC to each other and to the
gateway: proto-first contracts (via `buf`) mean a schema change is caught
by `buf breaking` in CI before it reaches a handler, not discovered at
runtime by a client guessing a JSON shape.

## Dev commands

```
make setup       # verify toolchain versions, install buf's local plugins
make gen         # regenerate gRPC/gqlgen stubs after editing a .proto or .graphqls file
make up/down/nuke  # local infra (Redis + Kafka + RabbitMQ) lifecycle
make up-full/down-full/nuke-full  # Phase 4: infra + all 6 app services via Docker (see below)
make test
make lint
```

Running a service directly:

```
cp .env.example .env   # then edit as needed
go run ./cmd/auth-service
go run ./cmd/skills-service
go run ./cmd/users-service
go run ./cmd/jobs-service
go run ./cmd/api-gateway
```

Every service degrades gracefully without a `DATABASE_URL` (there's no
live Supabase project yet at this phase) — they still start and serve
health checks, just report `/readyz` as not-ready and return `Unavailable`
from any RPC that needs a database.

## Running the full stack via Docker (Phase 4)

`docker/docker-compose.yml` builds and runs all 6 application services
(auth/skills/users/jobs/api-gateway, notification-service) on top of
`docker/docker-compose.infra.yml`'s Redis/Kafka/RabbitMQ — a clean
checkout reproducing the entire cross-phase flow via containers alone,
with no `go run`/`npm run` processes involved:

```
cp ../docker/.env.example ../docker/.env   # then fill in real DATABASE_URL values
make up-full     # builds + starts everything, waits on infra healthchecks
make down-full   # stops everything
make nuke-full   # stops everything and drops volumes
```

`docker/.env` supplies each of the 5 database-backed services' own scoped
`DATABASE_URL` (schema-per-service, so each needs a distinct value — see
`docker/docker-compose.yml`'s header comment and `docs/DECISIONS.md`'s
Phase 4 notes) plus optional secrets (JWT signing key, Google OAuth, rate
limit tuning); it's gitignored and never committed — only
`docker/.env.example` is. There is still no local Postgres service in
either compose file by design (single shared Supabase instance via
`DATABASE_URL`, not a container-per-service database) — `docker/.env.example`
documents how to point at a throwaway local `postgres:16-alpine` container
for local verification instead of real Supabase.

## Ports (dev defaults)

| Service | gRPC | HTTP (health/JWKS/GraphQL) |
|---|---|---|
| auth-service | 9001 | 8081 |
| skills-service | 9002 | 8082 |
| users-service | 9003 | 8083 |
| jobs-service | 9004 | 8084 |
| api-gateway | — | 8080 |
| notification-service | — | 8085 |

`cmd/allinone` reuses these same four backend port numbers, bound to
`127.0.0.1` instead of `0.0.0.0` (see `cmd/allinone/README.md`) — nothing
outside that one process can reach them. Its own public port is `$PORT`
(Render's convention, defaulting to 8080 locally), matching api-gateway's.

api-gateway (and `cmd/allinone`) also serve `/metrics` — Prometheus-format
counters/histograms for GraphQL request volume/latency/outcome and the
live WebSocket connection count, on the same HTTP port as `/query`. See
`internal/platform/telemetry` and `docs/DECISIONS.md`'s telemetry notes.

## Observability stack (tracing, logs, metrics dashboard)

```bash
docker compose -f ../docker/docker-compose.observability.yml up -d
```

Brings up Jaeger (distributed tracing, UI at http://localhost:16686), Loki+Promtail (centralized logging — tails every service's `/tmp/*.log` file from local-dev verification runs), Prometheus (scrapes api-gateway's `/metrics`), and Grafana (http://localhost:3300, anonymous admin, all three pre-provisioned as datasources). Set `OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317` on `api-gateway`/`allinone`/`skills-service` (or any other service you've instrumented) to start sending traces — see `internal/platform/tracing` and `docs/DECISIONS.md`'s tracing/logging notes for the full design and how this was verified.

**This whole stack is local/`kind`-only** — none of it runs in production (see `docs/DEPLOYMENT.md`). **Sentry** (`internal/platform/sentry`) is the one piece of observability that *does* run in production: set `SENTRY_DSN` (any service, including local dev) to get error tracking and log capture — every `cmd/*/main.go` degrades gracefully with it unset, same as every other optional dependency here. See `internal/platform/README.md`'s `sentry` section and `docs/DECISIONS.md`'s Sentry section for the full design.

## Deploying

Production runs `cmd/allinone`, not the five services above — see the
root README's "Deploying" section for the human steps (Supabase/Render/
Vercel account creation) and `docs/DECISIONS.md`'s Phase 7 notes for the
full reasoning and what was verified locally.

## Conventions

See `backend/CLAUDE.md` for Go-specific conventions (error handling
pattern, how to add a new service, where generated code lives).
