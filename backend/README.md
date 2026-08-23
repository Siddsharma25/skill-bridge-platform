# backend

This is the Go half of skill-bridge-platform: a set of gRPC microservices
fronted by a GraphQL gateway. It's a **learning project** — see the root
[`docs/DECISIONS.md`](../docs/DECISIONS.md) for the full architecture and
the reasoning behind every non-obvious choice before making structural
changes here.

## What's here (Phase 1a)

- **`cmd/auth-service`** — owns password credentials and JWT issuance.
  Speaks gRPC only (private network); see its own README for why.
- **`cmd/api-gateway`** — the public GraphQL edge. Holds no state of its
  own; every resolver is a thin pass-through to a backend gRPC service.
- **`internal/platform`** — the shared foundation every service uses:
  logging, request-ID propagation, health checks, graceful shutdown, the
  Postgres connection helper, and JWKS signing/verification. Built once,
  reused unchanged by every later service (`users-service`, `skills-service`,
  `jobs-service`) rather than re-solved per service.
- **`proto/`** — gRPC contracts, proto-first via `buf`. See `proto/README.md`.
- **`gen/`** — generated Go stubs from `proto/`. **Committed to git**, not
  regenerated as a hidden prerequisite — a clean checkout builds without
  running codegen first.
- **`migrations/`** — goose SQL migrations, one directory per service
  schema, plus `000_bootstrap.sql` (schemas + per-service Postgres roles,
  run once with an admin connection).

Later phases add `users-service`, `skills-service`, `jobs-service`, Kafka,
RabbitMQ, Redis caching, and a NestJS `notification-service` — see the
Phased Rollout in the architecture plan. Don't build ahead of the current
phase; each phase is independently demoable on purpose.

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
make up/down/nuke  # local infra (Redis for now) lifecycle
make test
make lint
```

Running a service directly:

```
cp .env.example .env   # then edit as needed
go run ./cmd/auth-service
go run ./cmd/api-gateway
```

Both services degrade gracefully without a `DATABASE_URL` (there's no live
Supabase project yet at this phase) — they still start and serve health
checks, just report `/readyz` as not-ready.

## Ports (dev defaults)

| Service | gRPC | HTTP (health/JWKS/GraphQL) |
|---|---|---|
| auth-service | 9001 | 8081 |
| api-gateway | — | 8080 |

## Conventions

See `backend/CLAUDE.md` for Go-specific conventions (error handling
pattern, how to add a new service, where generated code lives).
