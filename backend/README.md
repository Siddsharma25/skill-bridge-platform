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
- **`internal/platform`** — the shared foundation every service uses:
  logging, request-ID propagation, health checks, graceful shutdown, the
  Postgres connection helper, and JWKS signing/verification. Built once in
  Phase 1a, reused unchanged by every service added since.
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
make up/down/nuke  # local infra (Redis for now) lifecycle
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

## Ports (dev defaults)

| Service | gRPC | HTTP (health/JWKS/GraphQL) |
|---|---|---|
| auth-service | 9001 | 8081 |
| skills-service | 9002 | 8082 |
| users-service | 9003 | 8083 |
| jobs-service | 9004 | 8084 |
| api-gateway | — | 8080 |

## Conventions

See `backend/CLAUDE.md` for Go-specific conventions (error handling
pattern, how to add a new service, where generated code lives).
