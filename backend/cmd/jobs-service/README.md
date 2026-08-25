# jobs-service

Owns job postings (`jobs.jobs`), the skills each posting requires
(`jobs.job_required_skills`), and — as of Phase 2 — its own read-model of
every user's skills (`jobs.user_skill_snapshot`) and the matching worker's
output (`jobs.job_matches`).

## What it does (gRPC surface)

- `CreateJob(title, description, required_skill_ids[])` — inserts a job
  and its required-skill rows in one transaction. Unauthenticated in
  Phase 1b, same reasoning as `skills-service.CreateSkill` (see
  `docs/DECISIONS.md` — no role system exists yet). As of Phase 2, also
  publishes `job.posted` (job_id + required_skill_ids) to Kafka — the
  trigger for the in-process matching worker below.
- `ListJobs()` — returns every job, most-recently-created first, with
  `required_skill_ids` attached. Fetches jobs and required-skill rows in
  two queries total (jobs, then all their required skills grouped by
  `job_id` in memory) rather than one query per job — that's this
  service's own data, so there's no reason to pay an N+1 for it even
  though the *cross-service* skill-name resolution is left as a
  deliberate N+1 for the gateway (see below).
- `GetJob(id)` — same shape as one element of `ListJobs`, or `NotFound`.
- `ListJobMatches(job_id)` (Phase 2) — returns every `jobs.job_matches`
  row for a job, highest score first: the matching worker's output,
  exposed so the gateway's `Job.matches` field (and the Phase 2
  checkpoint) can be demonstrated through the actual API.
- Serves `grpc.health.v1.Health` plus HTTP `/healthz`/`/readyz`.

## Phase 2: the event flow, entirely in-process alongside the gRPC server

Three Kafka consumer groups run as goroutines in this same binary,
started and stopped alongside the gRPC server via
`internal/platform/shutdown` (see `cmd/jobs-service/main.go`):

1. **Snapshot projection** (`jobs-service.user-skill-snapshot`, topic
   `user.skills.updated`) — `internal/jobs/snapshot.go`. Each event
   carries a user's *entire* current skill list (not a delta); the
   handler replaces that user's `jobs.user_skill_snapshot` rows
   atomically (delete-then-insert in one transaction), so repeated or
   out-of-order delivery can never leave a duplicate or stale row. This
   is jobs-service's own event-carried-state-transfer copy of "what
   skills does this user have" — it never calls users-service directly
   (can't; different Postgres role, different schema — see
   `docs/DECISIONS.md`), and stays fully functional even if
   users-service is down.
2. **Matching worker** (`jobs-service.matching-worker`, topic
   `job.posted`) — `internal/jobs/matcher.go`. For each posted job, scores
   every candidate in the snapshot by skill overlap, idempotently upserts
   `jobs.job_matches` (`ON CONFLICT (job_id, user_id) DO UPDATE`, so
   redelivering the same event never creates a duplicate row), and
   publishes `job.matched` per match. Threshold and scoring are documented
   in `docs/DECISIONS.md`'s Phase 2 notes.
3. **Cross-service cache invalidator**
   (`jobs-service.cache-invalidator`, topic `skill.updated`) —
   `internal/jobs/cache_invalidation.go`. Evicts jobs-service's own
   `jobs:all` Redis entry whenever skills-service publishes a taxonomy
   change — the one place in this codebase where event-driven cache
   invalidation is the *right* tool (genuinely cross-service), as opposed
   to the write-through same-service `DEL` used everywhere else.

All three degrade gracefully like every other external dependency in this
codebase: if `KAFKA_BROKERS` is unset, each just doesn't start (logged
clearly), rather than the process failing to start.

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
