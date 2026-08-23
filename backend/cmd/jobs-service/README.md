# jobs-service

Owns job postings (`jobs.jobs`) and the skills each posting requires
(`jobs.job_required_skills`).

## What it does (Phase 1b)

- `CreateJob(title, description, required_skill_ids[])` — inserts a job
  and its required-skill rows in one transaction. Unauthenticated in
  Phase 1b, same reasoning as `skills-service.CreateSkill` (see
  `docs/DECISIONS.md` — no role system exists yet).
- `ListJobs()` — returns every job, most-recently-created first, with
  `required_skill_ids` attached. Fetches jobs and required-skill rows in
  two queries total (jobs, then all their required skills grouped by
  `job_id` in memory) rather than one query per job — that's this
  service's own data, so there's no reason to pay an N+1 for it even
  though the *cross-service* skill-name resolution is left as a
  deliberate N+1 for the gateway (see below).
- `GetJob(id)` — same shape as one element of `ListJobs`, or `NotFound`.
- Serves `grpc.health.v1.Health` plus HTTP `/healthz`/`/readyz`.

## What's explicitly not here yet

`jobs.job_matches` and `jobs.user_skill_snapshot` — the event-carried-
state-transfer projection that lets a matching worker score candidates
against their skills without jobs-service ever querying users-service's
schema directly — are Phase 2 additions, once Kafka exists to carry
`user.skills.updated`. See `docs/DECISIONS.md` for why jobs-service can't
just query users-service's database instead (schema-per-service isolation
forbids it) and why event-carried state transfer is the answer once Kafka
lands, not a synchronous cross-service RPC.

## Why required_skill_ids aren't resolved to names here

`required_skill_ids` are opaque skills-service IDs — jobs-service's
Postgres role cannot read `skills.*`, so it never validates or stores a
skill's name/category, just the ID. Resolving those IDs to display names
is the gateway's job: `Query.jobs { requiredSkills { name } }` fires one
call per job to skills-service today, a deliberate, small N+1 left for
Phase 1c's dataloader batching work rather than solved prematurely here.

## Degraded-start behavior

Same pattern as every service in this codebase: starts and serves health
checks even without `DATABASE_URL` set/reachable; every job RPC returns
`Unavailable` until a real database is configured.

## Local run

```
cd backend
go run ./cmd/jobs-service
# gRPC on :9004, HTTP (health) on :8084
grpcurl -plaintext localhost:9004 list
curl localhost:8084/healthz
```
