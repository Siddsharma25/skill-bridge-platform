# skills-service

Owns the platform's shared skill taxonomy — the one list of "skills" that
both users-service (a user's claimed skills) and jobs-service (a job's
required skills) reference by ID rather than storing their own copy.

## What it does

- `CreateSkill(name, category)` — inserts a new skill. Unauthenticated in
  Phase 1b (see docs/DECISIONS.md — no role system exists yet, so this is
  a known, accepted gap, not a bug). Also, as of Phase 2, publishes
  `skill.updated` (skill_id) to Kafka in addition to its existing
  `skills:all` Redis `DEL` — the `DEL` is write-through invalidation of
  skills-service's own cache, the Kafka publish is what lets jobs-service
  evict its own cache cross-service (see `internal/jobs`'s
  `skill.updated` consumer and `docs/DECISIONS.md`). Both happen; they
  invalidate two different services' caches, not the same one twice.
- `ListSkills()` — returns every skill, ordered by name. No pagination
  yet; not needed at this data scale.
- Serves `grpc.health.v1.Health` plus HTTP `/healthz`/`/readyz`, same as
  every other service.

## Why this is its own service, not folded into users-service or jobs-service

Both users-service and jobs-service need to reference skills, but neither
should own the canonical list — if either did, the other would need to
either duplicate the taxonomy or reach across a schema boundary it doesn't
have a role for. A dedicated service with its own schema/role is the
smallest change that avoids that: both consumers hold only a skill_id and
resolve display data through the gateway (a deliberate, small N+1 — see
docs/DECISIONS.md and users.proto/jobs.proto — left for Phase 1c's
dataloader work rather than solved here).

## Why it's the recommended future REST/OpenAPI candidate

Per the architecture plan, only one service gets `grpc-gateway` +
`protoc-gen-openapiv2` treatment (not all four, see docs/DECISIONS.md).
skills-service is the recommended target: one table, two RPCs, no
authentication logic to route around — the cleanest possible teaching
example when that phase lands. Not implemented yet.

## Degraded-start behavior

Same pattern as every service in this codebase: starts and serves health
checks even without `DATABASE_URL` set/reachable; `CreateSkill`/
`ListSkills` return a gRPC `Unavailable` status until a real database is
configured.

## Local run

```
cd backend
go run ./cmd/skills-service
# gRPC on :9002, HTTP (health) on :8082
grpcurl -plaintext localhost:9002 list
curl localhost:8082/healthz
```
