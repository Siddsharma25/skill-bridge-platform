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

## Phase 1b implementation notes

skills-service, users-service, jobs-service, and api-gateway's first authenticated GraphQL operations. Concrete choices made while building this slice:

### Lazy profile creation (users-service), and the Phase 1b -> 2 migration path

There's no Kafka `user.registered` consumer yet — that's Phase 2. Rather than have `GetProfile`/`UpdateProfile` fail for a `user_id` that's never been explicitly provisioned, both RPCs create an empty `users.profiles` row (`display_name = ''`, `bio = ''`) the first time either is called for an unrecognized `user_id`: `INSERT ... ON CONFLICT (user_id) DO NOTHING` followed by a re-`SELECT`, so two concurrent first-touch requests for the same user can't race into a duplicate-key error — one wins the insert, the other's `DO NOTHING` is a no-op, and both read back the same canonical row. Verified manually: calling `myProfile` for a brand-new user_id creates exactly one row (confirmed via direct SQL), a second `myProfile` call returns the same row without creating another.

This is a deliberate **stand-in**, not the intended long-term design. Once Phase 2 adds Kafka, auth-service will publish `user.registered` and users-service will consume it to provision the profile row at registration time instead of waiting for a first read/write. The row shape (`users.profiles`: `user_id`, `display_name`, `bio`) doesn't change between the two approaches — only *when* the row is created does, so nothing built on top of it (the GraphQL `Profile` type, the gateway resolvers) needs to change when Phase 2 lands.

### Table/schema names

- `skills.skills` (`id`, `name` unique, `category`).
- `users.profiles` (`user_id` PK, `display_name`, `bio`) and `users.user_skills` (`user_id`, `skill_id` composite PK, `proficiency`) — no FK from `user_skills.skill_id` to `skills.skills`, since users-service's role cannot read the `skills` schema at all (same isolation reasoning as everywhere else in this doc); an invalid `skill_id` is only ever caught when the gateway tries to resolve it for display.
- `jobs.jobs` (`id`, `title`, `description`) and `jobs.job_required_skills` (`job_id` FK to `jobs.jobs` with `ON DELETE CASCADE`, `skill_id`, composite PK) — `job_matches` and `user_skill_snapshot` are explicitly Phase 2 additions (see the "why jobs-service doesn't just query users-service's database" section above) and don't exist yet.

### GraphQL surface and the deliberate N+1

`Job.requiredSkills`, `Profile.skills`, and `UserSkill.skill` are all field resolvers (not plain data fields) — each is bound to a hand-written Go struct in `internal/gateway/graph/model/extra_models.go` (via `gqlgen.yml`'s `models:` section) carrying one hidden field (e.g. `Job.RequiredSkillIDs`) the resolver needs but that isn't itself a GraphQL field. Because skills-service has no single-ID lookup RPC (only `CreateSkill`/`ListSkills`), resolving a skill_id back to a name/category means fetching the whole taxonomy via `ListSkills` and filtering locally — once per parent object (once per `Job`, once per `UserSkill`). That's the deliberate small N+1 the task called for: acceptable at Phase 1b's data scale, marked `// TODO(phase 1c): dataloader` in `schema.resolvers.go`, and the actual fix batches every ID needed across a whole response into one call rather than one per object.

jobs-service itself avoids a *second*, needless N+1 on its own data: `ListJobs`/`GetJob` fetch every required-skill row for the returned job(s) in one additional query (grouped by `job_id` in memory), not one query per job — that data is jobs-service's own, so there's no reason to pay for it the same way the cross-service skill-name resolution does.

### JWT verification middleware (the first authenticated GraphQL operations)

Structured as `internal/gateway/authctx`, a new package (not folded into `internal/gateway/graph`, since it's needed by both `cmd/api-gateway/main.go` and the resolvers):

- `authctx.Middleware(jwksClient, log)` is a plain `net/http` middleware wrapping the gqlgen handler at `/query`. It extracts `Authorization: Bearer <token>`, verifies it via a new `jwks.Verify` function (the verifier-side companion to the existing `jwks.Sign` — parses the token, looks up the RSA public key by the token's `kid` header through the same `jwks.Client` api-gateway already fetched/cached at startup in Phase 1a), and — only on success — stores the JWT `sub` claim in the request context.
- It deliberately does **not** reject a request at the HTTP layer for a missing or invalid token, because `skills`/`jobs`/`createSkill`/`createJob` share the same `/query` endpoint and stay unauthenticated in Phase 1b (see "Known gaps" below); a stale or absent token on an unrelated public query shouldn't fail it. Instead, each resolver that actually requires a caller (`myProfile`, `updateProfile`, `addUserSkill`) calls a small `requireUserID(ctx)` helper (`internal/gateway/graph/helpers.go`) that returns a normal GraphQL error ("authentication required") if no verified user ID is present — keeping the rejection at the GraphQL-response layer, consistent with every other resolver error in this codebase, rather than a special-cased HTTP 401.
- The verified user ID is also forwarded as gRPC metadata (`user_id` key) on every call through users-service's client connection specifically (`authctx.UnaryClientInterceptor`, chained after the existing `requestid` interceptor) — matching the root plan's "verified claims are forwarded as gRPC metadata" design, even though today's users-service RPCs read `user_id` from an explicit request field (required by the proto contract) rather than the metadata. Kept consistent as the pattern any future service can rely on without re-verifying a token itself.
- Verified manually: `myProfile`/`updateProfile`/`addUserSkill` all reject with a GraphQL error (not an HTTP-level failure) when called with no `Authorization` header and when called with a garbage/invalid bearer token; all three succeed with a valid one.

### gqlgen resolver-file regeneration hazard

`gqlgen generate` only preserves the method bodies of resolver-embedding types it recognizes (`queryResolver`, `mutationResolver`, etc.); any other top-level declaration in `schema.resolvers.go` — a plain helper function, or a method on `*Resolver` itself — gets swept into a commented-out "one last chance to move this" block on every regeneration, and resolver *signatures* (parameter names specifically) get re-templated from the schema's argument names every time, even for a field whose body doesn't change. Concretely: `translateGRPCError`/`gqlError` (Phase 1a) and this phase's `resolveSkills`/`requireUserID`/`toModelSkill`/`toModelJob`/`toModelProfile` all got relocated into that commented block the moment any schema field changed and `make gen` ran again, and `Ping`'s `_ context.Context` (unused-param convention) and `CreateJob`'s `requiredSkillIds` (Go-style `IDs` renamed for `revive`) both got silently reverted to gqlgen's own naming on each regeneration too. Fix: any hand-written helper that isn't itself a resolver method now lives in `internal/gateway/graph/errors.go` / `helpers.go`, files `make gen` never touches; the two naming-convention tweaks are applied by hand after generating and are expected to need reapplying if the schema changes again and `make gen` is re-run.

### gqlgen as a pinned Go tool dependency

Running `go run github.com/99designs/gqlgen generate` failed with missing `go.sum` entries for gqlgen's own transitive deps (`golang.org/x/tools`, `github.com/goccy/go-yaml`, `github.com/urfave/cli/v3`) — expected, since nothing in this module's own package graph imports them, so plain `go get`/`go mod tidy` doesn't keep them pinned. Fixed with `go get -tool github.com/99designs/gqlgen`, which adds a `tool` directive to `go.mod` (Go 1.24+) telling `go mod tidy` to keep gqlgen and its dependency graph in the build list permanently, the same way `buf.gen.yaml`'s local `protoc-gen-go`/`protoc-gen-go-grpc` plugins are handled via `make setup`'s `go install`. Run `go run github.com/99designs/gqlgen generate` from `backend/internal/gateway/` specifically (where `gqlgen.yml` lives) — invoking it from `backend/` resolves the config's relative output paths against the wrong working directory and writes a stray `backend/graph/` tree instead of `internal/gateway/graph/`.

### goose: give every service its own migrations tracking table

Running all four services' `goose ... up` against one shared database (as the throwaway-Postgres end-to-end test, and Supabase itself, both do) surfaced a real gotcha: goose's default tracking table (`goose_db_version`, unqualified → the `public` schema) is keyed per *database*, not per `-dir`. Migrating auth first correctly recorded version 1; migrating users/skills/jobs against the same database next each saw that same version-1 row already in `public.goose_db_version` and silently reported "no migrations to run" — even though none of their own tables had been created yet. Fixed by giving each service its own schema-qualified tracking table (`goose -table auth.goose_db_version`, etc., now baked into `backend/Makefile`'s `migrate-up` target) — this also means the tracking table lives somewhere a scoped role can actually write, since a scoped role has no grants on `public` at all.

### Ports (dev defaults)

skills-service gRPC `9002` / HTTP `8082`; users-service gRPC `9003` / HTTP `8083`; jobs-service gRPC `9004` / HTTP `8084`. All overridable via `PORT`/`HTTP_PORT`, same as every other service — see `backend/.env.example`.

### Known gaps (Phase 1b, by design)

- **No role/authorization system yet.** `createSkill`, `createJob`, `skills`, `jobs`, and `job` stay unauthenticated — anyone can post a job or add a skill to the taxonomy. This is an accepted Phase 1b gap, not a bug to silently patch by inventing a role system early; a real authorization model is out of scope until a phase that actually specifies one.
- **`skill_id` isn't validated anywhere it's accepted as a bare string** (`users.user_skills.skill_id`, `jobs.job_required_skills.skill_id`) — neither users-service nor jobs-service can read skills-service's schema, so an invalid ID is only ever noticed when the gateway tries to resolve it for display and finds nothing.
- **`Job.requiredSkills`/`Profile.skills`/`UserSkill.skill` are a deliberate N+1** at the gateway, as described above — Phase 1c's dataloader work is the real fix.

### Verification performed beyond `go test`

Full flow exercised against a throwaway local Postgres container (`000_bootstrap.sql`, then each service's goose migration run with its own scoped role's tracking table, per the gotcha above): register → login → `createSkill` → `createJob` (requiring that skill) → `jobs` query confirms `requiredSkills` resolves → `myProfile` with a valid bearer token confirms lazy profile creation (verified via direct SQL that exactly one row exists) → a repeat `myProfile` call confirms no duplicate → `addUserSkill` then `myProfile` again confirms the skill appears with its resolved name/category → `myProfile`/`updateProfile`/`addUserSkill` all confirmed rejected (GraphQL error, not a transport failure) both with no `Authorization` header and with a garbage bearer token → cross-schema isolation reconfirmed negatively for all four new roles (`users_service` cannot `SELECT` from `auth.credentials`, `jobs_service` cannot read `skills.skills`, `skills_service` cannot read `jobs.jobs`) → all five services (auth, skills, users, jobs, api-gateway) started together and confirmed to shut down gracefully (SIGINT to all five, every process exited within ~2.5s, structured shutdown-sequence logs present for each, no zombie processes).
