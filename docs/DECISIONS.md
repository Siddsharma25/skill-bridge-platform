# Architecture Decisions

This is a learning-first project: the goal is genuine hands-on practice with microservices, gRPC, Kafka, RabbitMQ, Redis, Kubernetes, GraphQL, WebSockets, and NestJS — at $0 cost — not the leanest possible way to ship a job-matching app. Every non-obvious choice below is recorded with its trade-off so the reasoning survives past the code itself.

## Service split

Domain-based: `auth`, `users`, `skills`, `jobs` (all Go), `notification-service` (NestJS). Each owns its own Postgres schema on a single shared Supabase instance, enforced by a per-service Postgres role scoped via `GRANT` — real database-per-service isolation without needing 5+ paid/free-tier project instances.

## Why jobs-service doesn't just query users-service's database

Schema-per-service + per-role GRANTs mean jobs-service's role genuinely cannot read `users.*`. So the skill-matching worker can't "just look up" a candidate's skills. Instead: users-service publishes `user.skills.updated` to Kafka whenever a user's skills change; jobs-service consumes it and maintains its own local read-model (`jobs.user_skill_snapshot`). This is event-carried state transfer / CQRS — the thing that actually justifies Kafka's presence here, rather than reducing it to a glorified trigger bus. Side effect: the matching worker stays fully functional even if users-service is down, since it only ever reads its own snapshot.

## Communication: gRPC for gateway↔service, not REST

Proto-first via `buf` (`backend/proto/<service>/v1/*.proto` → generated stubs in `backend/gen/`, **committed to git**, not regenerated as a hidden CI prerequisite). `buf lint` + `buf breaking` run in CI — the actual payoff of being proto-first is catching an accidental contract break before merge, not after.

Auth: api-gateway is the *only* JWT verifier. auth-service exposes a JWKS endpoint rather than distributing a raw PEM secret everywhere, so key rotation doesn't require touching every service. Verified claims are forwarded as gRPC metadata; backend services trust the gateway rather than re-verifying.

## REST/OpenAPI: one service only, not all four

`grpc-gateway` + `protoc-gen-openapiv2` generate a REST reverse-proxy and OpenAPI spec from the same protos — but wiring this into all four services for a surface nobody calls isn't worth the tooling weight. Done properly on skills-service only, as the concrete learning exercise; `grpcurl`/`buf curl` covers debugging the rest, which is the more transferable gRPC skill anyway. Note: `protoc-gen-openapiv2` emits a spec *file*, not a served UI — a browsable page requires vendoring `swagger-ui` separately.

This debug/REST surface is gated behind `ENABLE_DEBUG_HTTP` (off by default) with the same shared-secret check gRPC metadata gets, so it can't become an unauthenticated bypass of the gateway's auth once anything is deployed publicly.

## Caching (Redis), and why invalidation isn't event-driven everywhere

Redis serves three purposes from day one: read-through cache for hot low-churn data (skill taxonomy, job search results), WebSocket pub/sub fan-out (below), and gateway rate limiting.

Invalidation is **write-through** (`DEL` in the same service's own write path) for same-service caches — e.g. skills-service deleting `skills:all` on its own mutation. A service round-tripping through its own Kafka event just to invalidate its own cache is strictly worse than calling `DEL` directly. The one place eventing *is* the right tool: jobs-service evicting `jobs:search:*` when it consumes skills-service's `skill.updated` — that's genuine cross-service invalidation.

## Real-time notifications: RabbitMQ hop kept as a deliberate choice

Services publish to RabbitMQ `notifications.realtime` → api-gateway consumes → republishes on Redis pub/sub, keyed by user → any gateway replica with that user's live subscription delivers it. A simpler design would skip RabbitMQ and have services publish straight to Redis pub/sub. The extra hop is kept intentionally as practice — it's a genuinely different pattern (command queue feeding a broadcast fan-out) — not an oversight.

## notification-service: plain `pg`, not Prisma

Prisma's `migrate` wants a shadow database and elevated privileges, and doesn't work cleanly through Supabase's transaction pooler without a separate `directUrl` + `multiSchema` config. For a service that owns exactly one table (`notification_log`), that's a multi-hour trap with no pedagogical payoff. Plain `pg` + one handwritten SQL migration instead.

## Kafka/RabbitMQ/Redis on Kubernetes: not Bitnami charts

Broadcom moved Bitnami's free image catalog to hardened/paid tiers in August 2025; versioned tags now live in the unmaintained `bitnamilegacy` repo. Using Kafka via the **Strimzi operator** and RabbitMQ via the **official RabbitMQ Cluster Operator** instead — both free, actively maintained, and arguably better practice than a Helm chart. Redis is a plain hand-written Deployment (15 lines, no chart needed). Every manifest pins `replicas: 1` and explicit resource limits — default operator settings will try to schedule multi-node clusters and OOM a 16 GB laptop.

## Production deployment: a single `allinone` binary, not 5 live services

Render's free tier is realistically one web service (background workers are paid-only, free services spin down after ~15 min idle). Running the full microservices mesh live for $0 isn't realistic, and a gateway fanning out to 4 sleeping services would serially cold-start all of them on one request. `backend/cmd/allinone` registers all four services' gRPC servers on loopback plus the gateway in a single process — one Render service, one cold start, zero public backend exposure. Kafka/RabbitMQ async flows exist locally and in kind, but not in production — the same codebase runs as a modular monolith (prod) or full microservices (local/kind, for learning), which is itself worth being able to explain.

## What's explicitly not built (and why)

- **No real email provider** — notification-service logs/records "would send X," since integrating a real provider is incidental to the messaging/NestJS practice goal.
- **No broker HA** — single replica for Kafka/RabbitMQ/Redis everywhere; resiliency at that scale isn't the point of this project.
- **No service mesh / mTLS** — plain private-network trust is the right altitude here.
- **No transactional outbox** — consumers are idempotent (upserts, dedupe keys) and RabbitMQ queues have a dead-letter exchange, but publish-after-write isn't atomic. Known, accepted gap for a learning project; a real production system would need one.
- **Jenkins pipeline is build-only** (checkout → test → docker build), no deploy stage — the deploy-to-kind step is already exercised manually in the Kubernetes phase, and GitHub Actions remains the only pipeline that actually gates anything.

## Learning docs

Each service gets its own short `README.md` covering what it does, why it's built this way, how it fits the rest of the system, and what to touch first to change it — written as the code is written, not batched at the end.

## Phase 1a implementation notes

Concrete choices made while building the first slice that weren't already pinned down above:

- **Ports (dev defaults):** auth-service gRPC `9001`, auth-service HTTP (health + JWKS) `8081`; api-gateway HTTP (GraphQL + Playground + health) `8080`. All overridable via `PORT`/`HTTP_PORT` env vars — see `backend/.env.example`.
- **JWT claims:** `sub` = user ID (GORM-generated UUID string), `email`, standard `iss`/`iat`/`exp`. No `user_role` claim yet — nothing in Phase 1a needs an authorization decision beyond "is this a valid token," so it's deferred rather than speculatively added.
- **Access token TTL:** 1 hour (`jwks.DefaultTTL`), no refresh-token rotation yet — there's nothing to rotate into until a later phase adds that flow (see the plan's Phase 1a bullet on the cross-origin auth story). This is the whole session lifetime for now.
- **bcrypt cost factor:** library default (10), not the max (31). High enough to be considered adequate as of 2026, low enough to keep local dev/CI fast. Revisit only if a real threat model calls for it.
- **JWT signing keys generated at startup when unset:** dev convenience (`JWT_PRIVATE_KEY_PEM` unset → auth-service generates a fresh RS256 keypair each process start). Explicitly not fine for production or multi-replica — a restart invalidates every outstanding token, and two replicas would mint incompatible keys. Set `JWT_PRIVATE_KEY_PEM` explicitly anywhere that isn't a single local dev process.
- **`buf generate` uses local plugins, not buf.build remote generation:** `protoc-gen-go`/`protoc-gen-go-grpc` installed via `go install` (see `backend/Makefile`'s `setup` target) rather than `buf.gen.yaml` `remote:` plugins. This keeps `make gen` and CI from depending on the BSR being reachable — a network call per proto build for a single-repo learning project isn't worth the fragility.
- **grpcurl reflection is enabled on every gRPC service** (`google.golang.org/grpc/reflection`), not gated behind an env flag in Phase 1a. This is what makes `grpcurl -plaintext <addr> list` work without shipping `.proto` files around, which is the plan's recommended debugging path for services without a REST surface. Safe on a private network; would need gating (like the `ENABLE_DEBUG_HTTP` REST surface) if a service's gRPC port were ever exposed publicly.
- **auth-service degrades to `Unavailable`, not a crash, when `DATABASE_URL` is unset or unreachable.** Verified manually: the service starts, `/healthz` returns 200, `/readyz` returns 503 with a clear reason, and `Register`/`Login` return a gRPC `Unavailable` status rather than the process failing to start. This was explicitly required since no live Supabase project exists yet.
- **`auth-service`'s Login RPC intentionally does not return a `user_id`** (only `LoginResponse.access_token` — see `proto/auth/v1/auth.proto`). A client that needs the ID can decode the JWT's `sub` claim locally; adding a redundant field to save one client-side JWT decode wasn't worth carrying in the contract.
- **`internal/auth`'s duplicate-email detection** checks for Postgres SQLSTATE `23505` via `errors.As` into `*pgconn.PgError` (GORM's postgres/pgx driver surfaces it this way), rather than a pre-check `SELECT` — avoids a TOCTOU race between the check and the insert.
- **Verification performed beyond what's required to just compile:** the full Register→Login→wrong-password→duplicate-email flow was exercised against a throwaway local Postgres container (via `000_bootstrap.sql` + the `auth_service` role + the goose migration, exactly as they'd run against real Supabase) — not just `go test`. This isn't wired into `go test ./...` itself (CI stays DB-free per the plan), but it's a stronger validation of the migrations/GORM/bcrypt/JWT path than unit tests alone would give, and it's how the `docker run --rm postgres` + `goose ... up` sequence in this note was confirmed to work end-to-end before assuming it would against Supabase later.
- **`.golangci.yml` targets golangci-lint's v2 config schema** (`version: "2"`), matching the v2.13.1 installed locally and pinned in CI (`golangci-lint-action@v7`, `version: v2.13.1`) — the v1 config schema (`linters-settings`, etc.) would silently no-op several options under v2.
- **`buf breaking --against '.git#branch=main'` runs with `continue-on-error: true`** in CI for now. On the very first few PRs there's limited history for it to diff against meaningfully; revisit removing the flag once `main` has accumulated a few real proto revisions and the check has been observed to behave as expected.
- **This worktree's branch was one commit behind `main`** (missing the frontend-restructure commit) when this phase started; fast-forwarded (`git merge main --ff-only`) before any backend work began so the backend scaffold sits on top of `frontend/` in its final location rather than the pre-restructure flat layout.
