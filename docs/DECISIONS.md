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
