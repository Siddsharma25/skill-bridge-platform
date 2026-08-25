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

## Phase 1c implementation notes

Google OAuth, Redis caching, gateway rate limiting, and the dataloader fix for Phase 1b's deliberate N+1. Concrete choices made while building this slice:

### Google OAuth account-linking design

`auth-service` gains two RPCs, `GetGoogleAuthURL` and `GoogleOAuthCallback` (`proto/auth/v1/auth.proto`), exposed at the gateway as `Query.googleAuthUrl(state: String!): String!` and `Mutation.googleOAuthCallback(code: String!): AuthPayload!` — both thin pass-throughs in `schema.resolvers.go`, the same shape as `register`/`login`.

- **Token exchange is the seam, not the database.** `internal/auth/oauth.go` defines `GoogleExchanger` (`AuthCodeURL` + `Exchange`) as the one thing `oauth_test.go` mocks; the real implementation (`googleOAuth2Exchanger`) wraps `golang.org/x/oauth2` and calls Google's actual token + `openidconnect.googleapis.com/v1/userinfo` endpoints. Trusting the userinfo endpoint's server-verified subject/email — never a client-supplied, signature-unchecked `id_token` claim — is what keeps this a real OAuth flow rather than "trust whatever the client says its Google account is."
- **Account-linking rule** (`linkOrCreateGoogleUser`), in priority order:
  1. An existing `auth.oauth_identities` row for this exact `(provider, subject)` wins outright — a repeat Google login reuses the `user_id` it already points to. Subject, not email, is authoritative here, since a Google Account's subject is stable even if its email later changes.
  2. Failing that, an existing `auth.credentials` row for the same email (created via password `Register`) is linked to — a new `oauth_identities` row is created pointing at that `user_id`, rather than silently creating a second account for the same person.
  3. Failing both, a new `auth.credentials` row (see below) and a new `oauth_identities` row are created together.
- **Google-only accounts still satisfy `auth.credentials.password_hash NOT NULL`** via `randomUnusablePasswordHash` — a bcrypt hash of 32 cryptographically random bytes nobody can ever know or guess, so `Login`'s `bcrypt.CompareHashAndPassword` simply never matches anything a real client could send. Setting a real password on a Google-only account later is a real, useful feature — explicitly out of Phase 1c's scope.
- **Degrades the same way a missing `DATABASE_URL` does elsewhere in this codebase.** `NewGoogleExchangerFromEnv` returns `(nil, false)` when `GOOGLE_OAUTH_CLIENT_ID`/`_SECRET`/`_REDIRECT_URL` aren't all set (no real Google Cloud OAuth client exists yet — an external dependency outside this repo's control); `auth-service` starts fine regardless, and both RPCs return a clear `FailedPrecondition` instead of the process failing to start or the RPCs panicking on a nil exchanger.
- **`state` is caller-owned, not server-owned.** `GetGoogleAuthURLRequest.state` is an opaque CSRF-protection value the frontend generates and must verify comes back unchanged from Google's redirect; auth-service is stateless (no server sessions anywhere in this codebase) and neither generates nor remembers it — it's forwarded verbatim into the consent URL and back out again.
- **Not built in Phase 1c, deliberately:** actually exercising this end-to-end needs a live Google Cloud OAuth client and a browser consent flow, neither of which exists in this environment. `oauth_test.go`'s 9 cases (new user, existing-identity reuse, existing-credential linking, exchange failure, missing subject/email, not-configured on both RPCs, etc.) are what stands in for that — real login through an actual Google account is the one Phase 1c checkpoint intentionally left unverified, same as the plan's Execution Notes called out from the start.

### Caching: keys, TTLs, and what's cached

The general write-through design (`DEL` in the same service's own write path, no event round-trip) is already decided above ("Caching (Redis), and why invalidation isn't event-driven everywhere"). Phase 1c is the first phase that actually builds it, in `internal/platform/cache` (a thin `*redis.Client` wrapper with `Get`/`Set`/`Del`/`Incr`, all degrading to a safe no-op when `REDIS_URL` is unset or unreachable) plus its two call sites:

- **skills-service:** `skills:all` caches the full `ListSkills` result (JSON-encoded, not the raw protobuf struct, so the cache format doesn't depend on protobuf-go's internal layout) for 1 hour — the taxonomy changes rarely, and `CreateSkill` explicitly `DEL`s the key in the same request that mutates it, so the long TTL never serves stale data on this instance; it's just insurance against a missed invalidation (e.g. a future direct-SQL edit). `GetSkillsByIds` (the dataloader's batch endpoint, see below) is deliberately **not** cached — it's already a targeted, batched lookup rather than a whole-taxonomy scan, and a second cache shape just for it wasn't judged worth it at this data scale.
- **jobs-service:** `jobs:all` (the `ListJobs` result) and `jobs:<id>` (per-job, for `GetJob`) both use a much shorter TTL (30 seconds) than skills' — job postings churn more than the skill taxonomy, and unlike skills-service's single invalidation point, jobs-service will gain a second, cross-service invalidation trigger in Phase 2 (`skill.updated` consumed off Kafka evicting `jobs:search:*`, per the plan) that doesn't exist yet; a shorter TTL bounds staleness in the meantime rather than relying on write-through alone. `CreateJob` `DEL`s `jobs:all` in the same request that mutates it; there's no `jobs:<id>` to invalidate for a brand-new job ID (nothing could have cached it before it existed), so `GetJob` on a newly created job relies on `jobsCacheTTL` alone until `UpdateJob` exists.
- Every cached value is a small hand-written JSON DTO, not the generated protobuf struct — decoding failures (e.g. a value written by a future incompatible cache-schema version) are logged and treated as a cache miss, falling through to the database, never a hard error.

### Dataloader: batching the gateway's cross-service N+1

Phase 1b left `Job.requiredSkills`, `Profile.skills` (via `UserSkill.skill`), and `UserSkill.skill` as a deliberate N+1 — skills-service had no single-ID lookup, so resolving an opaque `skill_id` meant fetching the *entire* taxonomy via `ListSkills` and filtering locally, once per parent object. Phase 1c's fix, in `internal/gateway/dataloader`:

- **`skills.proto` gains `GetSkillsByIds(ids) -> skills`** (any ID with no match is silently omitted, not an error — same "missing means absent" contract `resolveSkills` already had). This is the batch endpoint the loader's `BatchFunc` calls.
- **Library: `graph-gophers/dataloader/v7`, not gqlgen's `dataloaden`.** `dataloaden` is a `go generate` code generator producing one hand-typed loader file per (key, value) pair — a *third* codegen step next to `buf generate` and `gqlgen generate` this project already has to keep in sync (see `backend/CLAUDE.md`). `graph-gophers`'s generic `Loader[K, V]` (Go 1.18+ generics) gives the same compile-time type safety with zero extra generated files, which is the better fit here.
- **One `Loaders` per HTTP request, never shared across requests** — the classic dataloader bug (a resolved value or a `NotFound` result from one caller's request leaking into a completely unrelated caller's response) that a package-level or long-lived loader would create. `dataloader.Middleware` constructs a fresh `Loaders` and stores it in the request context for every incoming `/query` request; `FromContext` is what `resolveSkillsViaLoader` reads.
- **2ms wait window** (`waitWindow` in `dataloader.go`): long enough to collect every `Load()` call gqlgen issues while resolving sibling fields of one response (every `Job.requiredSkills` in a `jobs` list, since gqlgen resolves list-element fields concurrently by default), short enough to be imperceptible against real gRPC call latency. Proven, not assumed: `dataloader_test.go` fires 15 concurrent `Load()` calls (5 simulated jobs × 3 skill IDs each, drawn from a pool of 4, deliberately overlapping) and asserts exactly one batched `GetSkillsByIds` call resulted.
- **Falls back to the Phase 1b whole-taxonomy scan (`resolveSkills`) when no `Loaders` is present in context** — a resolver invoked outside `dataloader.Middleware` (e.g. a resolver-level test that doesn't go through the HTTP handler) still works correctly, just without batching.
- **Live-endpoint proof, not just the loader-level unit test:** `internal/gateway/graph/dataloader_integration_test.go` drives the actual gqlgen HTTP handler (wrapped in `dataloader.Middleware`, exactly as `cmd/api-gateway/main.go` composes it) with a real `{ jobs { requiredSkills { ... } } }` query across 5 jobs sharing 4 distinct skill IDs, asserting skills-service's fake `GetSkillsByIds` was invoked exactly once for the whole response. This was additionally reconfirmed against the *live* running services (not just the test suite): temporary logging in `skills-service`'s `GetSkillsByIds` handler, a throwaway Postgres + Redis, all five services running together, 4 real skills, 5 real jobs created via GraphQL mutations with deliberately overlapping `requiredSkillIds`, then one `jobs { requiredSkills { name } }` query against the live gateway — skills-service's log showed exactly one `GetSkillsByIds` call carrying all 4 IDs, not five separate `ListSkills` calls. The temporary logging was removed before committing; the behavior it proved is what `dataloader_integration_test.go` continues to assert going forward.

### Rate limiting: fixed-window, Redis-backed, keyed by identity not just address

`internal/gateway/ratelimit`, wrapping the GraphQL handler in `cmd/api-gateway/main.go` (composition order: `authctx.Middleware` → `ratelimit.Middleware` → `dataloader.Middleware` → the gqlgen handler — rate limiting must run after `authctx` so it can see a verified user ID, and before the GraphQL handler so a rejected request never reaches gqlgen at all).

- **Fixed-window, not token-bucket.** A token bucket gives smoother burst handling, but needs either a Lua script or a multi-command Redis transaction to stay atomic; a fixed window is a single `INCR` plus a conditional `EXPIRE` on the increment that creates the key (`cache.Client.Incr`) — one round trip, trivially correct, and the classic "burst at the window boundary" imprecision a fixed window has doesn't matter at this project's scale or threat model. Revisit only if real traffic ever makes that boundary effect a genuine problem.
- **Default: 100 requests per key per 60-second window** (`ratelimit.DefaultConfig`), overridable via `RATE_LIMIT_REQUESTS`/`RATE_LIMIT_WINDOW_SECONDS` without a code change. Generous enough not to bother a normal interactive client (GraphQL Playground, a frontend re-fetching occasionally) while still bounding a runaway loop or scripted abuse — a starting point to tune once real traffic patterns exist, not a load-tested number.
- **Key: verified user ID when available, else client IP** (`ratelimit:user:<id>` / `ratelimit:ip:<addr>`, computed in `rateLimitKey`). Preferring the user ID once `authctx` has verified one avoids two failure modes a pure-IP key has: several users behind one NAT/corporate proxy sharing a single budget, and one user rotating IPs to dodge the limit. An unauthenticated caller only ever has an IP to be identified by, so that's the fallback. `clientIP` reads `X-Forwarded-For`'s first entry when present (for once this sits behind a real proxy/load balancer) before falling back to `RemoteAddr`.
- **Fails open on a Redis outage**, exactly like `cache.Client`'s other methods — `Incr` returning `ok=false` (Redis unset, unreachable, or any other Redis-side error) makes the middleware log a warning and let the request through rather than rejecting traffic it can't actually count. Consistent with this codebase's established resilience pattern everywhere else Redis or the database is involved: degrade the feature, never fail the request.
- **Rejection: a plain HTTP 429 with a `Retry-After` header, not a GraphQL-layer error.** Unlike `authctx`'s `requireUserID` (a per-resolver authorization decision that belongs inside a specific GraphQL response, since an unrelated public query on the same `/query` endpoint shouldn't fail for it), rate limiting is a transport-level concern that applies *before* any resolver — or even the GraphQL request itself — is parsed. There is no GraphQL operation yet to attach a `{"data": null, "errors": [...]}` envelope to at the point this middleware rejects, and every GraphQL client already knows how to handle a non-200 HTTP status from its underlying fetch call.
- **Verified live, not just via unit test:** the gateway was run against a real Redis with an aggressive limit (3 requests / 8-second window) — the first 2 requests within the window succeeded, the next 3 were rejected with HTTP 429 and the documented JSON body, and a request issued after the 8-second window elapsed succeeded again, confirming the fixed window actually recovers rather than latching shut. Separately, Redis was stopped mid-run and 5 more requests were fired at the gateway — every one still returned HTTP 200, with `"rate limiter unavailable (redis unreachable/disabled); allowing request through"` logged for each, confirming the fail-open path holds under a real connection failure, not just a mocked one.
- **Unit tests** (`internal/gateway/ratelimit/ratelimit_test.go`) cover: rejection once the limit is exceeded and that it persists across further requests in the same window; recovery after the window elapses (simulated via a fake limiter's window reset, not a real sleep); fail-open behavior when the limiter reports unavailable; and that the user-ID-vs-IP key preference actually produces separate budgets (two different authenticated users behind the same IP each get their own budget; the same user from two different IPs shares one).

### GraphQL surface additions

`googleAuthUrl(state: String!): String!` and `googleOAuthCallback(code: String!): AuthPayload!`, both documented above. No other schema changes this phase — `Job.requiredSkills`/`Profile.skills`/`UserSkill.skill` keep their Phase 1b field signatures; only what's *behind* them changed (dataloader instead of a per-object `ListSkills` scan).

### gqlgen regeneration hazard, reconfirmed

Phase 1b's note on this (`gqlgen generate` re-templating resolver parameter names from the schema's argument names on every run, and relocating any non-resolver top-level declaration into a commented-out block) bit again here: adding `googleAuthUrl`/`googleOAuthCallback` to `schema.graphqls` and running `go run github.com/99designs/gqlgen generate` from `internal/gateway/` reverted `CreateJob`'s `requiredSkillIDs` back to schema-cased `requiredSkillIds` and `Ping`'s `_ context.Context` back to a named-but-unused `ctx`, exactly as documented — reapplied by hand immediately after, per the established workflow, rather than treated as a surprise.

### Known gaps (Phase 1c, by design)

- **Real Google OAuth login was not exercised end-to-end.** No live Google Cloud OAuth client exists in this environment (an external, out-of-repo dependency — see the plan's Execution Notes). `GetGoogleAuthURL`/`GoogleOAuthCallback` are fully implemented and unit-tested against a faked `GoogleExchanger`; wiring a real client ID/secret and completing a browser consent flow is the one Phase 1c checkpoint left for whenever that credential exists.
- **`GetSkillsByIds` is not cached** (see Caching section above) — an intentional scope call, not an oversight, revisit only if profiling ever shows it matters.
- **Rate limiting is IP/user-keyed only, with no per-operation cost weighting** — a cheap `ping` query and an expensive `jobs { requiredSkills { ... } }` query currently cost the same one request against the budget. Fine at this project's scale; a production system serving real traffic would likely want to weight mutations or deeply-nested queries more heavily.

### Verification performed beyond `go test`

All of Phase 1a/1b's flow (register → login → `createSkill` → `createJob` → `jobs` query → `myProfile` lazy-create → `updateProfile` → `addUserSkill` → `myProfile` again → unauthenticated rejection, both missing-header and garbage-token) reconfirmed with **zero regressions** against a fresh throwaway Postgres + Redis and all five services running together, plus everything new this phase: live dataloader batching (above), live rate-limit rejection and window recovery (above), live Redis-outage fail-open for both rate limiting and skills/jobs caching, a `skills` query's second identical call confirmed served entirely from cache via `redis-cli monitor` (only a `GET skills:all`, no write-through `SET`/`DEL`), and all five services confirmed to shut down gracefully together on `SIGINT` (every process exited within ~1s of signal, structured shutdown-sequence logs present for each, no zombie processes). `go build/vet/test ./...`, `golangci-lint run ./...`, `buf lint`, and both `buf generate` and `gqlgen generate` (each re-run and confirmed to produce no undocumented diff, modulo the resolver-naming hazard above) all clean from `backend/`.

## Phase 2 implementation notes

Kafka, the real event flow: `user.skills.updated` → jobs-service's `user_skill_snapshot` projection; `job.posted` → the in-process matching worker → `job_matches` (idempotent upsert) → `job.matched`; `skill.updated` → jobs-service's cross-service cache eviction. Concrete choices made while building this slice:

### Kafka client library: `github.com/twmb/franz-go`

Chosen over Confluent's `confluent-kafka-go` (wraps `librdkafka` via cgo — a real cross-compilation/Docker multi-stage-build cost this project didn't want to pay for four Go services, see this codebase's existing `Dockerfile.<service>` multi-stage-to-distroless pattern) and over Shopify/IBM's `sarama` (still fine, but less actively maintained and with a clunkier consumer-group API than franz-go's `PollFetches` loop). franz-go is pure Go, no cgo, actively maintained, and its generic-free `kgo.Client` handles both producing and consumer-group consuming through one client type — a good fit for `internal/platform/kafka`'s thin-wrapper goal (`Producer`/`Consumer`, mirroring `internal/platform/cache.Client`'s shape). See `backend/internal/platform/kafka/producer.go` and `consumer.go`.

**Discovered during this phase's live verification, not something to reproduce by inspection alone:** franz-go does **not** request auto topic creation on a metadata fetch by default, even though the local broker is configured with `auto.create.topics.enable=true` (see `docker/docker-compose.infra.yml`). Without `kgo.AllowAutoTopicCreation()` explicitly passed to `kgo.NewClient`, the *first* publish to any brand-new topic in this codebase failed with `UNKNOWN_TOPIC_OR_PARTITION` (confirmed via `skills-service`'s logs during live testing) — the client was asking the broker "does this topic exist" and explicitly telling it not to create one if not, then failing on the produce. Fixed by adding `kgo.AllowAutoTopicCreation()` to `NewProducerFromEnv`'s client options (see `producer.go`'s comment). Consumers were left without this option — a consumer shouldn't be the thing that creates a topic (a stray typo'd topic name in a consumer subscription creating garbage topics would be a worse failure mode than "this consumer group has nothing to consume yet").

### Event payload format: JSON, not protobuf

A plain JSON envelope per topic (`internal/platform/kafka/events.go`: `UserSkillsUpdated`, `JobPosted`, `JobMatched`, `SkillUpdated`, each a small `json`-tagged struct) rather than protobuf messages. This project is proto-first for gRPC specifically because `buf breaking` catches an accidental *synchronous contract* break before merge (see the Communication section above) — that payoff is much smaller for a Kafka payload, where the producer and consumer are already different services deployed independently and schema evolution tolerance (new optional field, consumer ignores what it doesn't recognize) is the normal expectation for an event stream regardless of encoding. Adding `.proto` files, `buf generate` wiring, and a second `gen/`-adjacent output path for four small event shapes wasn't judged worth the tooling weight this phase — a documented JSON shape (this section, plus each struct's doc comment) is sufficient practice. If protobuf-for-events is wanted later as a deliberate exercise, `events.go`'s structs are a 1:1 map to what the `.proto` messages would look like.

### Topic names

Exactly as named in the plan: `user.skills.updated`, `job.posted`, `job.matched`, `skill.updated` (constants in `internal/platform/kafka/events.go`). Every topic is auto-created on first publish (see the franz-go note above) rather than pre-declared — single partition, single broker, fine for local dev; a real deployment would pre-declare topics with an explicit partition count instead of relying on this.

### Consumer group IDs

`jobs-service.user-skill-snapshot`, `jobs-service.matching-worker`, `jobs-service.cache-invalidator` — one per responsibility, all three running as separate goroutines/franz-go clients inside the single `jobs-service` binary (see `cmd/jobs-service/main.go`'s `startConsumer` helper). Separate consumer groups (not one group multiplexing three topics) so each can be scaled, restarted, or fail independently, and so a slow/broken handler in one never blocks another's partition assignment.

### Matching algorithm and threshold

Simple skill-overlap scoring, deliberately not ML, per the plan's Scope Cuts. `internal/jobs/matcher.go`'s `CandidatesForSkills` does one grouped SQL query (`SELECT user_id, count(*) FROM jobs.user_skill_snapshot WHERE skill_id IN (...) GROUP BY user_id`) against the snapshot table, returning every user with **at least one** overlapping skill. The threshold (`matchThresholdOverlap = 1`) is "at least one shared skill" — the simplest rule that produces a non-empty, demonstrable match set without inventing a percentage cutoff this project has no traffic data to justify. `score` is still computed and stored as `overlap / len(required_skill_ids)` even though the threshold itself doesn't use it, so a future ranking/UI change has something to sort by without a schema change (this is exactly what `ListJobMatches`/`Job.matches` return highest-score-first).

### Idempotency: delete-then-insert for the snapshot, upsert for matches

- **Snapshot** (`internal/jobs/snapshot.go`'s `gormSnapshotStore.ReplaceUserSkills`): one transaction, `DELETE FROM jobs.user_skill_snapshot WHERE user_id = ?` then a bulk insert of the event's current skill list. A full replace, not an upsert-plus-stale-row-cleanup — simpler to reason about and correct by construction for "the event says this is the user's entire current list" (event-carried state transfer, not a delta).
- **Matches** (`internal/jobs/matcher.go`'s `gormMatchStore.UpsertMatch`): `ON CONFLICT (job_id, user_id) DO UPDATE SET score = ..., matched_at = ...`.

Both are proven idempotent two ways: a unit test with an in-memory fake store (`snapshot_test.go`'s `TestHandleUserSkillsUpdated_ReprocessingSameEventIsIdempotent`, `matcher_test.go`'s `TestHandleJobPosted_ReprocessingSameEventIsIdempotent`), and a live proof against the real Postgres tables — republishing the exact same `job.posted`/`user.skills.updated` payload via `kafka-console-producer.sh` inside the running Kafka container and confirming via direct SQL that the row count stayed at 1 (with `matched_at`/timestamps refreshed to the new delivery, proving it was actually reprocessed, not skipped).

### Cross-service cache invalidation: `jobs:all` only, no Redis `SCAN`

jobs-service's `skill.updated` consumer (`internal/jobs/cache_invalidation.go`) evicts only the `jobs:all` key. The plan's wording ("evicts jobs-service's own `jobs:search:*`-style Redis cache keys") anticipates a pattern-based eviction, but jobs-service's actual Phase 1c cache shapes are just `jobs:all` (the full `ListJobs` result) and `jobs:<id>` per job — there is no skill-name-based search cache yet to match a `jobs:search:*` pattern against. `skill.updated` only carries a `skill_id`, not the set of job IDs that reference it, so evicting the per-job `jobs:<id>` entries individually isn't wireable without either a Redis `SCAN` (a new capability `cache.Cache` doesn't have, and one more moving part for a cache entry that already has a 30s TTL bounding its staleness — see `jobsCacheTTL`) or jobs-service tracking a skill_id → job_id reverse index it doesn't otherwise need. `jobs:all` is the one cache shape actually analogous to a "search results" cache today, so it's the one evicted; the 30s TTL is judged sufficient to bound staleness on the rest.

### GraphQL `Job.matches`: added, not left to direct SQL

The plan left this as a judgment call. Added `ListJobMatches(job_id)` to `jobs.proto` (`buf generate`d) and `Query.job { matches { userId score matchedAt } }` to the gateway schema (`gqlgen generate`d) — it was a small, natural addition (one new RPC that only queries jobs-service's own table, one resolver, no new cross-service call), and it lets the Phase 2 checkpoint ("creating a job asynchronously produces queryable match rows") be demonstrated through the actual public API instead of only via `psql`. `Job.matches` is not batched through the dataloader the way `Job.requiredSkills` is: unlike skill-ID resolution (which every `Job` in a list needs, against a service with no single-ID lookup), a `ListJobMatches` call is already a single targeted per-job query, so there's no redundant per-object work to remove by batching it.

### `buf generate`/`gqlgen generate`, the known resolver-regeneration hazard, reconfirmed a third time

Both `Job.matches`' resolver stub and the schema hazard documented in Phase 1b/1c notes hit again exactly as described: adding `matches` to `schema.graphqls` and running `gqlgen generate` reverted `CreateJob`'s `requiredSkillIDs` param back to schema-cased `requiredSkillIds` (with the resolver *body* still referencing the old name, so the generated file didn't even compile until fixed by hand) and `Ping`'s `_ context.Context` back to a named-but-unused `ctx`, while correctly preserving the hand-written body of the pre-existing `Job.requiredSkills` resolver and inserting a `panic("not implemented")` stub for the brand-new `Job.matches` one. Fixed by hand immediately after, per the established workflow — not treated as a surprise, and not worth trying to engineer away this phase.

### Verification performed beyond `go test`

Brought up the throwaway local Postgres (a fresh container, `000_bootstrap.sql` then all four services' goose migrations run against one admin connection, including the two new jobs-service migrations) + Redis + the new Kafka service (`docker compose -f docker/docker-compose.infra.yml up -d`, official `apache/kafka:3.9.0` image, KRaft mode) and all five Go services running together. Exercised, live, on top of Phase 1's full flow with zero regressions:

- **users-service → jobs-service projection:** registered a user, called `addUserSkill` through the gateway, confirmed via `users-service`'s logs that no publish error was logged (i.e. `user.skills.updated` was published successfully) and, moments later, `jobs-service`'s snapshot consumer logged `"updated user skill snapshot"` — confirmed via direct SQL that exactly one `jobs.user_skill_snapshot` row now existed for that user/skill.
- **job.posted → matching worker → job_matches → job.matched:** created a skill and a job requiring it through the gateway, confirmed jobs-service's matching-worker consumer logged `"processed job.posted event"` with `matched_users: 1`, confirmed via direct SQL that a `jobs.job_matches` row existed for the user who had the skill and **no row** for a second registered user who didn't, and confirmed via `kafka-topics.sh --list` inside the Kafka container that `job.matched` existed as a topic (i.e. had actually been published to).
- **skill.updated → cross-service cache eviction:** created a skill through the gateway and confirmed jobs-service's cache-invalidator consumer logged `"evicted jobs:all cache after skill.updated"` immediately after — this is the live proof of the one genuinely cross-service cache-invalidation case in this codebase.
- **Idempotent reprocessing, live, not just unit-tested:** republished the identical `job.posted` payload (same `job_id`) directly onto the topic via `kafka-console-producer.sh` inside the Kafka container and confirmed via SQL that `jobs.job_matches` still had exactly one row for that `(job_id, user_id)` pair, with `matched_at` refreshed to the new delivery's timestamp — proving reprocessing updates in place rather than duplicating. Repeated the same proof for `user.skills.updated` against `jobs.user_skill_snapshot`. A deliberately malformed message (`"hello-world-test"`, not valid JSON) was also published to `job.posted` during this and confirmed logged as `"failed to decode job.posted event; skipping"` at Error level without crashing the consumer or blocking the next (valid) message from being processed — the poison-message handling `internal/platform/kafka.Consumer.Run`'s doc comment describes.
- **The actual point of the design — kill users-service, matching still works:** `kill -9`'d the running `users-service` process (not a graceful stop — simulating a real crash), then created a second job requiring the same skill through the gateway. `createJob` succeeded (jobs-service never talks to users-service for this), the matching worker processed `job.posted` and produced a correct `jobs.job_matches` row for the same user, entirely from jobs-service's own `user_skill_snapshot` projection — confirmed via SQL. This is `docs/DECISIONS.md`'s "Side effect: the matching worker stays fully functional even if users-service is down," proven, not just asserted.
- **Graceful shutdown, all five services + three consumer groups:** `SIGINT` to all five processes; every process exited within roughly 100ms of signal (structured shutdown-sequence logs confirmed for each), and `kafka-consumer-groups.sh --describe --state` afterward showed all three of jobs-service's consumer groups in state `Empty` with `0` members — a clean `LeaveGroup`, not a stale member waiting out a session timeout, which is what a missing/broken shutdown sequence would have left behind.
- `go build/vet/test ./...`, `golangci-lint run ./...`, and `buf lint` all clean from `backend/`; `buf generate` (for the new `jobs.proto` RPC/messages) and `gqlgen generate` (for `Job.matches`/`JobMatch`) both re-run, with the one documented resolver-regeneration hazard above fixed by hand as expected.

### Known gaps (Phase 2, by design)

- **The first publish to a brand-new topic on a cold broker still costs a few seconds** (metadata fetch → broker auto-creates the topic → produce retry succeeds) even with `kgo.AllowAutoTopicCreation()` set — observed as ~10s on the very first `createSkill` call against a freshly started Kafka container before that option was added, and near-instant for every call after a topic already exists. Acceptable for local dev (four topics, created once); a real deployment would pre-create topics and never pay this.
- **`job.matched` has no consumer yet** — nothing in this codebase subscribes to it. It's published (and its existence as a topic was confirmed during live verification) because the architecture plan calls for it and it's the observable proof the matching worker's output is a real event, not just a database write; a future phase (e.g. real-time notifications) is the natural place to add a consumer.
- **No transactional outbox**, same documented gap as everywhere else in this project — a crash between the primary write (Postgres) and the Kafka publish in `AddUserSkill`/`CreateSkill`/`CreateJob` would silently drop that one event. Consumers are idempotent (this section's whole point) so a *redelivered* event is safe, but a *never-sent* event isn't self-healing without a later event for the same key.

## Phase 3 implementation notes

RabbitMQ, the first broker in this codebase besides Kafka: auth-service's `Register` publishes a best-effort `notifications.email` welcome event; notification-service (NestJS, `backend/notification-service/`) consumes it, records a "would send" welcome email (log line + a deduped `notifications.notification_log` row), with a dead-letter exchange for anything unprocessable. Concrete choices made while building this slice:

### RabbitMQ client library: `amqp091-go` (Go) / `amqplib` (TypeScript), not `@nestjs/microservices`

The Go producer side uses `github.com/rabbitmq/amqp091-go` (the maintained fork of the archived `streadway/amqp`), the same "thin wrapper, degrade gracefully when unconfigured" shape `internal/platform/kafka` already established. On the NestJS side, the consumer uses plain `amqplib` via `RabbitmqConnectionService`/`RabbitmqConsumer`, not `@nestjs/microservices`' RMQ transport — that transport's `noAck`/`ackAll`/handled-message helpers don't cleanly expose the per-message "nack without requeue, so it dead-letters" control this service's DLQ behavior depends on, whereas amqplib's `channel.ack`/`channel.nack(msg, false, false)` do exactly that.

### DLX/DLQ topology: declared by both sides, not owned by one

`notifications.dlx` (a direct exchange) → `notifications.email.dlq` (bound to it), and `notifications.email` declared with `x-dead-letter-exchange`/`x-dead-letter-routing-key` arguments pointing at both. Declared identically — same names, same arguments — by *both* auth-service's producer (`internal/platform/rabbitmq/topology.go`, `DeclareTopology`) and notification-service's consumer (`src/rabbitmq/topology.ts`, `declareTopology`), rather than picking one "owner" and having the other assume it already exists. This diverges from the plan's "pick one side" framing deliberately: AMQP's `queue.declare`/`exchange.declare` are idempotent as long as every caller passes identical arguments, and a publish to the default exchange with a routing key that doesn't match any existing queue is silently dropped, not queued or errored — so whichever service happens to start first in local dev (container/process start order isn't guaranteed) would otherwise risk losing the very first message published before the other side ever declares the queue. Both sides declaring it defensively costs nothing and closes that hazard entirely.

### Retry policy: zero retries before DLQ

A malformed/unparseable `notifications.email` message is nacked without requeue — straight to the DLQ, no retry loop, no backoff queue. A message that fails to parse can never succeed no matter how many times it's redelivered, so a retry would only delay the inevitable DLQ trip. This is a deliberate scope cut for a learning project's local broker (see Known Gaps below), not an oversight — a production system would more likely want a bounded retry count with backoff before dead-lettering, to absorb genuinely transient failures distinct from poison messages.

### Idempotency: `message_id` as the dedupe key, `ON CONFLICT DO NOTHING`

`notifications.notification_log.message_id` (a UUID generated by auth-service's producer per notification, carried unchanged through the AMQP message body) has a UNIQUE constraint; the consumer inserts with `ON CONFLICT (message_id) DO NOTHING` and acks either way. This is the "idempotency key from the message" approach, chosen over a unique constraint on a natural key like `(user_id, event_type)` because a future event type (e.g. a "job matched" notification) could legitimately fire more than once for the same user — `message_id` uniqueness survives that; a natural-key constraint wouldn't. At-least-once redelivery (a consumer restart before the original ack landed, or a manual republish while debugging) is safe: it produces zero additional rows, not a duplicate "welcome" notification.

### A valid message with the database down: ack, not nack

`NotificationsService.recordWelcomeEmail` returns `'db_unavailable'` (not a thrown error) when Postgres isn't configured or reachable, and `RabbitmqConsumer` acks the message in that case rather than nacking it to the DLQ. The message itself is perfectly valid — a transient or unconfigured database (Supabase's free-tier auto-pause is an expected reality for this project, not an edge case) shouldn't be treated the same as a poison message that can never succeed. The tradeoff, accepted deliberately: a notification that arrives while the database is down is genuinely lost (acked, not persisted, not retried) rather than sitting in the DLQ for manual replay. A production system with a real email provider would likely want the opposite tradeoff (nack-and-retry on `db_unavailable`, reserve the DLQ for parse failures) since a genuinely lost customer-facing email is worse than temporary redelivery pressure on the broker; this project's "no real email provider, log-only" scope cut makes the simpler "just ack it" behavior an acceptable loss.

### The consumer-attachment bug found during live verification, and its root cause

Live verification caught a real startup race, not a logic bug: `RabbitmqConsumer.onModuleInit` (which reads `RabbitmqConnectionService.channel` to start consuming) and `RabbitmqConnectionService.onModuleInit` (which asynchronously dials RabbitMQ, opens a channel, and declares topology) both run as plain NestJS `OnModuleInit` hooks — and NestJS calls every provider's `onModuleInit` within a module *concurrently* via `Promise.all` (`@nestjs/core/hooks/on-module-init.hook.js`), not sequenced by constructor-injection dependency order. A naive `RabbitmqConsumer.onModuleInit` that synchronously read `this.connection.channel` at its own hook-time would frequently see `null`, because `RabbitmqConnectionService`'s `amqp.connect()`/`createChannel()`/`declareTopology()` chain was still in flight — confirmed live: the consumer's "not configured" warning logged *before* the connection service's "connection established" log, and the RabbitMQ management UI showed `0` consumers on `notifications.email` despite `/readyz` already reporting connected.

Fix: `RabbitmqConnectionService` exposes a `ready: Promise<void>` that resolves once its own `onModuleInit` has finished one way or the other (channel live, or left `null` after a failed/missing `RABBITMQ_URL`), resolved in a `finally` block so it resolves on both the success and failure path. `RabbitmqConsumer.onModuleInit` `await`s `ready` before reading `channel`, which makes it observe the *finished* state of the connection setup instead of racing it. Live re-verification after the fix: notification-service's startup log now shows `"rabbitmq connection established and topology declared"` immediately followed by `"consuming notifications.email"` with a consumer tag, and the management API (`GET /api/queues/%2F/notifications.email`) confirms `consumers: 1`, `state: running` on every restart.

The exact same hazard turned up a second time on the *shutdown* path during this phase's live verification, since `onModuleDestroy` uses the identical `Promise.all` concurrent-execution shape (`on-module-destroy.hook.js`, same `callOperator`). `RabbitmqConnectionService.onModuleDestroy` closing the channel could race `RabbitmqConsumer.onModuleDestroy` calling `channel.cancel(consumerTag)` on that same channel — observed live as a caught-but-misleading `WARN: error cancelling rabbitmq consumer during shutdown` / amqplib `IllegalOperationError: Channel closing` on an otherwise-clean `SIGINT`. Harmless in effect (closing a channel already implicitly cancels any consumer on it, so nothing actually leaked or hung), but wrong per the code's own stated intent ("close the channel, which cancels any active consumer, before the connection") and noisy on every graceful shutdown. Fixed the same way as the startup race, but inverted: `RabbitmqConnectionService.registerBeforeClose(fn)` lets `RabbitmqConsumer` register its own cancel step to run first and deterministically inside `RabbitmqConnectionService.onModuleDestroy`, rather than `RabbitmqConsumer` implementing its own racy `OnModuleDestroy`. Re-verified live: a clean `SIGINT` after the fix produces no warning in notification-service's log at all.

### Verification performed beyond `go test`/`npm test`

Brought up a throwaway local Postgres container + Redis/Kafka/RabbitMQ (`docker compose -f docker/docker-compose.infra.yml up -d`), ran `000_bootstrap.sql` then all five services' goose migrations (`make migrate-up`, now including `migrations/notifications`) against it, and started all six services together (auth, skills, users, jobs, api-gateway, notification-service) — each service's own scoped Postgres role, per the existing per-service-role pattern.

- **Consumer attachment, live** (see above): confirmed via notification-service's startup log and the RabbitMQ management API that `notifications.email` shows exactly one running consumer immediately after startup, on repeated restarts.
- **Cross-language proof:** registered a new user (`phase3-verify@example.com`) through the live GraphQL gateway; confirmed auth-service's log showed `"registered new credential"` and the RabbitMQ producer configured/publish path, notification-service's log showed `"would send welcome email"` then `"processed notifications.email message"` with `result: "inserted"`, and a direct SQL query against `notifications.notification_log` showed exactly one row with a matching `message_id` between the two services' logs.
- **DLQ proof, live:** published a deliberately malformed message (`"{not-valid-json-at-all"`) directly to `notifications.email` via the RabbitMQ management API. Confirmed notification-service logged `"malformed notifications.email message; routing to DLQ"` at Error level with the offending raw payload, the message landed in `notifications.email.dlq` (queue depth 1 via the management API), the main queue's consumer count stayed at 1 (didn't crash, didn't hang), and a second live registration immediately afterward was still processed and inserted normally — proving the consumer keeps consuming valid messages after a DLQ event, not just that the DLQ receives the poison message.
- **Regression smoke test:** register → login → `createSkill` → `addUserSkill` → `createJob` → `myProfile` all confirmed working end-to-end through the gateway with zero regressions from Phase 3's changes.
- **Phase 2's Kafka matching flow, reconfirmed:** created a skill, added it to a user (`user.skills.updated` → jobs-service's snapshot projection, confirmed via SQL), then created a job requiring that skill *after* the snapshot existed — jobs-service's matching worker logged `"processed job.posted event"` with `candidates_scored: 1, matched_users: 1`, and a `jobs.job_matches` row existed for the expected `(job_id, user_id)` pair, confirmed via SQL.
- **Graceful shutdown, all six services:** `SIGINT` to all six processes; every process exited within roughly 100ms of signal, structured shutdown-sequence logs present for each of the five Go services, and notification-service's NestJS shutdown hooks completed with no warnings (post-fix — see the shutdown-race note above) and no zombie processes (confirmed via `lsof` on every service port after shutdown).
- `go build/vet/test ./...`, `golangci-lint run ./...`, and `buf lint` all clean from `backend/`; `npm test` (19 tests, 3 suites), `npx tsc --noEmit`, and `npm run lint` all clean from `backend/notification-service/`.

### Known gaps (Phase 3, by design)

- **No reconnect loop.** If RabbitMQ is unreachable at startup (or the connection drops mid-run), both auth-service's producer and notification-service's consumer log and give up for that process's lifetime — no retry-with-backoff, no re-declaring the consumer once the broker comes back. Restarting the process is the only recovery, same "no broker HA/reconnect" stance this project already takes for Kafka and Redis (see Scope Cuts). A production deployment would want `amqp091-go`'s/`amqplib`'s connection-recovery hooks wired in.
- **No transactional outbox**, same documented gap as Phase 2's Kafka publishes — a crash between `Register`'s Postgres insert and the RabbitMQ publish would silently drop that one welcome notification. The consumer's idempotent dedupe (this section's whole point) protects against a *redelivered* message being double-processed, not against a message that was never sent in the first place.
- **Zero retries before DLQ** (see above) is a deliberate simplicity-over-resilience call for a learning project's local broker; a transient processing failure that isn't actually a poison message (there isn't one today — `recordWelcomeEmail` never throws, it returns `'db_unavailable'` instead) would still go straight to the DLQ under the current `handleMessage` logic if one existed, with no distinction from a truly malformed payload.
- **A `db_unavailable` welcome notification is genuinely lost, not queued for replay** (see above) — an accepted tradeoff specific to this project's "no real email provider" scope cut, not a general-purpose pattern to reuse if this service ever sends anything customer-facing for real.

## Phase 3.5 implementation notes

GraphQL subscriptions over WebSocket, the real-time notifications flow the plan's Architecture Overview called for: services publish to RabbitMQ's `notifications.realtime` → api-gateway consumes it and republishes on Redis pub/sub, keyed by user → any live `onNotification` GraphQL subscription for that user receives it, no polling. Concrete choices made while building this slice:

### The verification-trigger decision: `job.matched`, not `Register`'s welcome publish, and why

The plan names both `auth-service` (on `Register`) and `jobs-service` (on `job.matched`) as `notifications.realtime` publishers, and both are implemented (`internal/auth/server.go`'s `Register`, `internal/jobs/matcher.go`'s `HandleJobPosted`) — architecturally complete, both unit-tested. But only `job.matched` is used as this phase's **live** verification trigger, for a structural reason the plan itself calls out: `Register`'s publish happens synchronously, inside the RPC, before the caller has even received the access token `Register` returns — so a client cannot possibly have a valid token to authenticate an `onNotification` subscription with until after the publish already happened. Redis pub/sub has no replay buffer (unlike Kafka, there's no consumer-group offset to rewind); a `PUBLISH` with no live `SUBSCRIBE` on that channel at that instant is simply and silently lost. Verified live, not just reasoned about: a throwaway diagnostic test that registered a user and opened a subscription as fast as two sequential local HTTP/WebSocket calls could go *occasionally* won the race and received its own "welcome" notification (see `internal/gateway/livetest/subscription_live_test.go`'s package doc comment) — but "occasionally, if nothing scheduled a goroutine unluckily" is not a repeatable verification trigger, which is exactly the point.

`job.matched` doesn't have this problem: a user can register, log in, and open a genuinely listening `onNotification` subscription — now holding a real token — *before* a job matching their skills is ever created. `internal/gateway/livetest/subscription_live_test.go`'s `TestOnNotification_JobMatchedLivePush` does exactly this (register → createSkill → addUserSkill → wait for the Kafka snapshot projection to catch up → **open the subscription** → createJob → assert a `next` message arrives), and is the actual, repeatable, deterministic proof this phase's live verification relies on.

### WebSocket auth: `connection_init`, not an `Authorization` header

A WebSocket upgrade request doesn't carry a bearer header on every subsequent message the way HTTP does, so `graphql-ws` (gqlgen's `transport.Websocket`) instead has the client send auth once, in the `connection_init` message's JSON payload. `cmd/api-gateway/main.go`'s `wsInitFunc` (a `transport.WebsocketInitFunc`) is called exactly once per accepted connection, before any `subscribe` message on it is processed:

- Reads the token from either `Authorization` (via gqlgen's own `InitPayload.Authorization()`, which already checks both header-cased and lowercased keys, with an optional `"Bearer "` prefix stripped) or a bare `token` field — accepting both the HTTP-header-shaped convention most `graphql-ws` client libraries already use for `connectionParams` (Apollo, urql, the `graphql-ws` npm package) and a plainer alternative for a client that doesn't want to fake an HTTP header inside a JSON body.
- Verifies it via `authctx.VerifyToken` — a small helper extracted from `authctx.Middleware`'s inline logic specifically so the HTTP and WebSocket paths share one implementation rather than two copies that could drift.
- **Rejects the connection outright on failure** (returns a non-nil error, which makes gqlgen close the socket before ever sending `connection_ack`) — a deliberate departure from `authctx.Middleware`'s HTTP behavior, which never rejects at the transport layer since `/query` serves both authenticated and unauthenticated operations. Every use of the WebSocket transport today is `onNotification`, which always requires an authenticated caller, so there's no mixed case to preserve here. The close is tagged with code `4401` (`graphql-ws`'s own protocol-recommended "Unauthorized" code, set via `transport.WithWebsocketCloseCode`) — verified live to reach the wire on the modern `graphql-transport-ws` subprotocol most of the time, though gqlgen's `WriteClose` runs the actual close handshake in a background goroutine (to avoid blocking transport teardown on a slow/absent peer — see `websocket_coderws.go`), so there's an inherent, harmless race between that goroutine completing and the underlying connection tearing down some other way; either outcome still means the client never receives `connection_ack` and the socket closes, which is the property that actually matters and is what `TestOnNotification_InvalidTokenRejected` asserts (a non-ack outcome, not a specific close code).
- **Never leaks one connection's identity into another's.** gqlgen constructs one `wsConnection` struct per accepted socket, and `InitFunc` is called once per connection with that connection's own base context — the verified user ID returned becomes `wsConnection.ctx`, which every `subscribe` on *that* connection derives from. There is no shared/global mutable state involved anywhere in this path.

### The bridge: RabbitMQ → Redis, channel naming, and no DLQ on purpose

`internal/gateway/realtime.Bridge` is api-gateway's first RabbitMQ consumer written in Go (every prior consumer in this codebase — `notifications.email`'s — lives in NestJS/notification-service). `internal/platform/rabbitmq.Consumer` (a new type, `consumer.go`) mirrors `kafka.Consumer`'s shape (`NewXFromEnv` degrades gracefully, `Run(ctx, handler)` blocks until cancelled, `Close()` tears down) rather than inventing a third consumer pattern.

- **Channel naming:** `realtime:user:<user_id>` (`internal/gateway/realtime.UserChannel`) — one function both the bridge (publishing) and the `onNotification` resolver (subscribing) call, so the two sides can't drift apart independently.
- **`notifications.realtime` has no dead-letter exchange**, unlike `notifications.email` — a deliberate divergence from that queue's established pattern, not an oversight (see `topology.go`'s doc comment on the constant). This queue feeds an already-lossy, best-effort UI ping: even a *successfully* consumed and republished message is silently dropped if no gateway replica has a live subscriber on that channel at that instant (the whole "why `job.matched`, not `Register`" reasoning above). A DLQ exists to make a lost message *recoverable*; there is nothing meaningful to recover a stale real-time notification into days later, so paying for the DLX/DLQ machinery here buys nothing the email queue's DLQ genuinely buys. A malformed message is logged at Error and dropped (acked, not nacked-to-nowhere) — same "log and move on, never crash the consumer" discipline as everywhere else — verified live by publishing a deliberately malformed payload directly to `notifications.realtime` via the RabbitMQ management API and confirming the consumer logged the failure and kept processing the next (valid) message normally afterward.
- **Redis pub/sub abstraction:** `internal/platform/cache` gained a third narrow interface, `PubSub` (alongside the existing `Cache`/`RateLimiter`), with `Publish`/`Subscribe` methods on `*cache.Client` — same degrade-gracefully convention (`Subscribe` returns `(nil, false)` when Redis is disabled, exactly like `Get`/`Incr` reporting a miss/unavailable rather than erroring). `Subscription` (the handle `Subscribe` returns) is satisfied directly by `*redis.PubSub` with zero adapter code, by matching its `Channel(...redis.ChannelOption) <-chan *redis.Message` signature rather than a narrower no-argument one.

### The `onNotification` resolver: subscribe, forward, and — the part most likely to have a subtle bug — clean up correctly

`schema.resolvers.go`'s `OnNotification` (hand-written after `gqlgen generate` produced the stub — see the resolver-regeneration hazard section below) calls `requireUserID` (defense in depth; the WebSocket connection is already rejected at `connection_init` if unauthenticated, so this never actually fires in practice for this field), opens a Redis `SUBSCRIBE` on `realtime.UserChannel(userID)`, and spawns one goroutine that forwards decoded messages into the channel gqlgen streams back to the client, in a loop `select`ing on both `sub.Channel()` and `ctx.Done()`.

**Cleanup happens in exactly one place** — that goroutine's `defer`, triggered by `ctx.Done()` — closing the output channel and calling `Subscription.Close()` (Redis `UNSUBSCRIBE`) unconditionally. `ctx` here is the GraphQL operation context gqlgen derives from the WebSocket connection's own context, which gqlgen cancels when the client sends a `complete`/`stop` message *or* when the underlying connection drops (see `transport.Websocket`'s `subscribe`/`closeOnCancel` handling) — so both a graceful unsubscribe and an abrupt client disconnect take the same cleanup path, with no separate code to keep in sync.

**Verified live, not just reasoned about — this was the task's explicit ask, and it's a real, checkable thing, not a theoretical property:** `TestOnNotification_DisconnectReleasesRedisSubscription` opens a real subscription, confirms via Redis's own `PUBSUB NUMSUB realtime:user:<id>` that exactly one subscriber exists, then calls `conn.CloseNow()` (an abrupt disconnect with no `complete` message — the more realistic and more dangerous leak scenario than a clean unsubscribe) and polls `PUBSUB NUMSUB` until it confirms the count drops back to zero. Passed cleanly (`internal/gateway/livetest/subscription_live_test.go`).

### GraphQL schema addition

```graphql
type Notification { id: ID!, type: String!, message: String!, createdAt: String! }
type Subscription { onNotification: Notification! }
```

Deliberately minimal — no `jobId`/`score` on the wire (those live in `internal/platform/rabbitmq.RealtimeNotification`, the broker-side payload, but aren't re-exposed at the GraphQL layer); a client that wants more detail about a `job_match` notification can follow up with an ordinary `job(id: ...)` query. `createdAt` is a plain `String!` (RFC3339), matching this schema's existing convention for `JobMatch.matchedAt` rather than introducing a GraphQL custom scalar for one field.

### `gqlgen generate`'s resolver-regeneration hazard, reconfirmed a fourth time

The hazard documented in Phase 1b/1c/2's notes (parameter names re-templated from the schema's argument casing, `Ping`'s `_ context.Context` reverted to a named-but-unused `ctx`) hit again exactly as expected: adding `onNotification`/`Subscription` to `schema.graphqls` and running `gqlgen generate` reverted `CreateJob`'s `requiredSkillIDs` param back to schema-cased `requiredSkillIds` and `Ping`'s `_ context.Context` back to `ctx`, while correctly preserving `OnNotification`'s hand-written body (it doesn't touch a resolver it doesn't recognize as newly-generated a second time) and adding the new `subscriptionResolver` type/`Resolver.Subscription()` method on its own. Fixed by hand immediately after each of the two `gqlgen generate` runs this phase needed (one for the initial stub, nothing further after — this note exists so a *third* run, if the schema changes again later, isn't a surprise).

### The real bug this phase's live verification actually caught: an unbounded RabbitMQ `Channel.Close()` can hang forever

Not something obvious from reading `amqp091-go`'s godoc, and exactly the kind of thing the task's "pay particular attention to whether shutdown hangs" instruction was for. Live testing (SIGINT to api-gateway with a `notifications.realtime` consumer that had processed at least one message) showed the entire `shutdown.Wait` sequence hang **indefinitely** — well past its documented ~10s bound — confirmed via `SIGQUIT` (which dumps every goroutine's stack before the process exits): the main goroutine was parked inside `amqp091-go.(*Channel).call`, reached from `Consumer.Close()`'s `ch.Close()`.

Root cause: `Channel.Close()` is a synchronous AMQP RPC (send `channel.close`, block for the broker's `channel.close-ok` reply) dispatched through the same per-channel goroutine that also delivers `Consume` messages. `Consumer.Run`'s loop simply stops reading its `deliveries` channel on `ctx.Done()` without telling the broker to stop sending (no `basic.cancel`) — if amqp091-go's internal dispatch goroutine is mid-send to that now-abandoned, unbuffered `deliveries` channel when `Close()` is called, it can never get around to processing the next frame (the close-ok reply `Close()` is waiting on), and `Close()` blocks forever.

Also discovered: `http.Server.Shutdown`'s own godoc says it plainly — *"Shutdown does not attempt to close nor wait for hijacked connections such as WebSockets. The caller of Shutdown should separately notify such long-lived connections of shutdown and wait for them to close, if desired."* This codebase does not do that (a live subscription is simply severed when the process exits, with no clean WebSocket close frame sent to the client first) — an accepted, documented gap consistent with this project's existing "no fancy reconnect/graceful-notify machinery" stance (see Scope Cuts), not something this phase built.

**The fix:** `internal/platform/rabbitmq/close.go`'s `closeTimeout` (3s) + `closeWithTimeout` — both `Producer.Close` and `Consumer.Close` now run their `Channel.Close`/`Connection.Close` calls in a background goroutine and give up (logging a `Warn`, not blocking) after `closeTimeout`, the same "run it in the background, bound it, log and move on" shape gqlgen's own `coder-websocket` transport adapter already uses for exactly this class of problem (a synchronous close handshake that can block on a slow/absent peer). `TestCloseWithTimeout_BoundsAHangingClose`/`_ReturnsImmediatelyOnFastClose` (`internal/platform/rabbitmq/rabbitmq_test.go`) prove the bound holds without a live broker. A more complete root-cause fix (explicitly `ch.Cancel`-ing the AMQP consumer subscription before `Consumer.Run` returns) was considered and rejected: `Cancel` is *also* a synchronous RPC on the same channel, and issuing it from the same goroutine that just decided to stop reading `deliveries` risks the identical wedge — the bounded `Close()` is the one thing this codebase actually needs to guarantee (shutdown makes bounded progress), and it does, unconditionally, regardless of the specific way a channel might get wedged.

### Known gaps (Phase 3.5, by design)

- **No reconnect loop**, same accepted gap as Phase 3's RabbitMQ producer/consumer and every other broker client in this codebase (see Scope Cuts) — if RabbitMQ or Redis drops mid-run, api-gateway's realtime consumer/bridge and every open subscription simply stop working until the process restarts.
- **A subscribed client's WebSocket connection is severed abruptly on gateway shutdown/restart**, not closed with a clean `graphql-ws` `complete`/close-frame handshake first — see the `http.Server.Shutdown` doc-comment finding above. A real client would need its own reconnect logic to recover, which this project doesn't build (no frontend WebSocket client exists yet either).
- **`Register`'s `notifications.realtime` publish is real but effectively undemonstrable live**, for the structural reason explained above — it's exercised by unit tests (`internal/auth/server_test.go`'s existing `fakePublisher`-based tests cover the call happens; a dedicated assertion on its payload shape wasn't judged necessary given `job.matched`'s equivalent coverage already proves the wire format end-to-end) but not by a live WebSocket proof, unlike `job.matched`.
- **No transactional outbox**, same documented gap as every other broker publish in this codebase — a crash between a primary write (the `job_matches` upsert, the account creation) and the `notifications.realtime` publish would silently drop that one realtime ping. Lower-stakes than the equivalent gap for `notifications.email`, since this queue was already documented above as feeding a best-effort, already-lossy notification.
- **The 4401 WebSocket close code isn't guaranteed to reach every client on every rejection** (see the WebSocket-auth section above) — an inherent race in gqlgen's async `WriteClose`, not something this phase's code controls. The functional guarantee (no `connection_ack`, ever, for an invalid token) holds regardless and is what `TestOnNotification_InvalidTokenRejected` actually asserts.

### Verification performed beyond `go test`

Brought up the throwaway local Postgres (reused an existing fully-migrated container from a prior phase's verification) + Redis/Kafka/RabbitMQ (`docker compose -f docker/docker-compose.infra.yml up -d`) and all five Go services, built from the final code and run via `go run`-equivalent binaries with each service's scoped role/env, exactly as prior phases' notes describe.

- **The live WebSocket subscription proof** (`internal/gateway/livetest/subscription_live_test.go`, build-tag `live`, run via `go test -tags live -count=1 ./internal/gateway/livetest/... -v` — `-count=1` matters: these tests hit a live external stack, and Go's test cache doesn't know that, so a repeat invocation with no source changes will silently replay a stale cached PASS without touching the network at all unless caching is disabled): a hand-rolled `graphql-transport-ws` client (using `github.com/coder/websocket` directly, not gqlgen's own transport code, so this test proves the wire protocol independently of the server's implementation) registered a real user, created a real skill, added it to the user, waited for jobs-service's Kafka snapshot projection to converge, **opened a genuinely authenticated `onNotification` subscription**, then created a job requiring that skill through the ordinary `createJob` mutation — and asserted a `next` message carrying `{"type":"job_match", "message":"A new job matches your skills!", ...}` arrived within a bounded timeout. It did, in ~3 seconds, confirmed on three separate full runs (different random user/skill/job IDs each time, so not a cached replay) and once more immediately after deliberately publishing a malformed message to `notifications.realtime` (proving the consumer survives a poison message and keeps relaying real ones).
- **The negative auth case**, live: `TestOnNotification_InvalidTokenRejected` connects with a garbage `Authorization` value in `connection_init` and confirms no `connection_ack` is ever received (the connection closes instead) — run repeatedly, observed both with and without the 4401 close code actually reaching the client (see the known-race note above), always correctly rejected.
- **The Redis-subscription-leak test**, live: `TestOnNotification_DisconnectReleasesRedisSubscription` confirmed `PUBSUB NUMSUB` goes 0 → 1 (subscription opened) → 0 (after an abrupt `CloseNow()`, no clean unsubscribe message) — no leak.
- **Zero regression on every earlier phase's flows**, reconfirmed live after this phase's changes: `register` → `login` → `myProfile` (lazy-create), `createSkill`, `createJob` with `requiredSkills`/`matches` resolving correctly through the dataloader and the Phase 2 matching worker — all via direct GraphQL calls against the live gateway, all succeeding with the exact shapes prior phases' notes describe. `notifications.email`'s RabbitMQ topology (queue + DLX + DLQ, with its dead-letter arguments) was confirmed unchanged and correctly declared via the RabbitMQ management API (`GET /api/queues/%2F`) — notification-service itself (unmodified this phase, NestJS, needs `npm install`) wasn't started live in this session to keep the verification loop fast, since no code path of its own changed and its own topology declaration is independently confirmed intact.
- **Graceful shutdown, live, all five Go services together:** `SIGINT` to auth/skills/users/jobs/api-gateway simultaneously — auth-service, skills-service, users-service, and jobs-service each completed their full shutdown sequence in well under 100ms (jobs-service's three Kafka consumer groups included). api-gateway's shutdown took up to ~3 seconds (bounded by the `closeTimeout` fix above, when its RabbitMQ consumer's `Close()` hit the hang described above) — a small, bounded, and logged delay, not the indefinite hang this same scenario produced before the fix (confirmed by reproducing the pre-fix hang first, via `SIGQUIT`'s goroutine dump, then re-testing after the fix and confirming the bound holds). Confirmed separately that an api-gateway shutdown with **no** open subscription completes in ~3ms — the delay is specific to having had an active realtime consumer, not a general regression.
- `go build/vet/test ./...`, `golangci-lint run ./...`, and `buf lint` all clean from `backend/` (`buf generate` also re-run — no diff, since no `.proto` file changed this phase). `gqlgen generate` re-run for the schema addition, with the documented resolver-regeneration hazard above fixed by hand as expected.

## Phase 4 implementation notes

Assembling everything built in Phases 1a–3.5 into one full local
docker-compose stack: `docker/docker-compose.yml` (new) adds the 6
application services on top of `docker/docker-compose.infra.yml`
(unchanged in shape, one config fix below). This phase is integration
only — no new application features, no k8s, no Jenkins.

### DATABASE_URL: `docker/.env` + Compose variable substitution, not the `env_file:` keyword

Schema-per-service means each of the 5 database-backed services needs its
own distinct `DATABASE_URL` value (its own scoped Postgres role — see
`migrations/000_bootstrap.sql`). Compose's `env_file:` keyword injects one
file's keys verbatim into a container's environment and can't fan one
`DATABASE_URL=...` line out into 5 different values, so `docker/.env`
(gitignored; `docker/.env.example` committed) instead defines distinctly
named variables (`AUTH_DATABASE_URL`, `SKILLS_DATABASE_URL`,
`USERS_DATABASE_URL`, `JOBS_DATABASE_URL`, `NOTIFICATION_DATABASE_URL`)
and each service in `docker-compose.yml` maps its own into the container's
plain `DATABASE_URL` via `${...}` interpolation. Compose resolves `${...}`
from whatever `--env-file` points at, which is why the documented
invocation (`backend/Makefile`'s new `up-full`/`down-full`/`nuke-full`
targets) passes `--env-file ../docker/.env` explicitly rather than relying
on Compose's default project-directory `.env` lookup — that default is
tied to the current working directory (`backend/`, per this project's
existing invocation convention), not to where the compose files
themselves live (`docker/`), so it wouldn't find the file without the
explicit flag. `REDIS_URL`/`KAFKA_BROKERS`/`RABBITMQ_URL` are hardcoded
directly in `docker-compose.yml`'s `environment:` blocks instead (not
sourced from `docker/.env` at all) since they're not secrets and don't
vary by environment the way a database credential does — only their
*value* changes (service name instead of `localhost`), which the compose
file itself is the right place to own.

### The real finding: Kafka's advertised listener broke every containerized publish, silently and indefinitely

This is the one genuine bug this phase's live verification surfaced, not
just a packaging exercise. `docker-compose.infra.yml`'s Kafka service
(unchanged since Phase 2) advertised a single listener as
`localhost:9092` — correct for every earlier phase, where every Kafka
client (backend services via `go run`, `kafka-topics.sh` from a shell
inside the Kafka container) reached the broker either from the host's own
network namespace or from inside that same container, both satisfied by
one address.

Phase 4 breaks that assumption: skills-service/users-service/jobs-service
now run *as containers* on the compose network and need to reach Kafka by
its service name (`kafka:9092`). The symptom, live: every `createSkill`
call through the containerized gateway hung **indefinitely** — no
timeout, no error, nothing logged by skills-service at all. Diagnosis
(each step deliberately isolating one layer, since the absence of any
error log ruled out the obvious causes first):

1. Confirmed skills-service's `/healthz`/`/readyz` were fine and its
   gRPC/Kafka/DB startup logs showed no error — the process was healthy,
   just the RPC itself hung.
2. Confirmed via direct SQL that the skill row **was** being inserted
   into Postgres on every hung call — ruling out the database and
   proving the hang was strictly after the primary write.
3. Bypassed the gateway entirely with a `grpcurl` call straight to
   `skills-service:9002` (via a throwaway container on the same compose
   network) — still hung, ruling out anything gateway-side.
4. Confirmed plain TCP reachability both host→container (`nc -zv
   localhost 9002`) and container→container
   (`docker exec sbp-notification-service nc -zv skills-service 9002`,
   and separately `nc -zv kafka 9092`/`redis 6379`) — all open. Network
   plumbing itself was fine.
5. Confirmed the broker was genuinely healthy and accepting writes via
   `kafka-console-producer.sh` run from a shell *inside* the Kafka
   container — instant, no issue. The broker itself was not the problem.
6. Confirmed `skill.updated` had auto-created successfully as a topic
   (`kafka-topics.sh --list`/`--describe` showed it with a live leader
   and ISR=[1]) — so a produce **had** succeeded at least once, ruling
   out the previously-documented "first publish to a new topic" cold-start
   cost (Phase 2's notes) as the cause of an indefinite hang specifically.

That combination — write succeeds, broker healthy, topic exists, network
open, yet every subsequent RPC still hangs forever — pointed at exactly
one remaining thing: `internal/platform/kafka.Producer.Publish` calls
`kgo.Client.ProduceSync(ctx, record)` synchronously (see
`internal/platform/kafka/producer.go`), and a Kafka client's *seed*
connection succeeding is not the same as its *actual* produce path
working. franz-go's seed connection to `kafka:9092` (Docker DNS resolves
the service name fine) succeeds, but the broker's first Metadata response
— which tells the client which address to send the real Produce request
to for that partition's leader — said `localhost:9092`, since that's
literally what `KAFKA_ADVERTISED_LISTENERS` told it to say. Inside a
container, `localhost:9092` resolves to that container itself, not Kafka.
`ProduceSync` then retried against an address nothing was listening on,
bounded only by the caller's own context deadline (none, from a plain
`curl` with no `-m` flag — hence "indefinitely" in practice) rather than
by any client- or broker-side error, which is exactly why nothing was
ever logged: the retry loop itself never *failed*, it just never
succeeded either.

**Fix:** `docker-compose.infra.yml`'s Kafka service now advertises two
listeners instead of one — `INTERNAL` (`kafka:9092`, for anything on the
compose network) and `EXTERNAL` (`localhost:29092`, for a host process
like `go run ./cmd/skills-service` outside Docker). The published host
port moved from `9092:9092` to `29092:29092` accordingly (port 9092
inside the container is now the INTERNAL listener's in-container port,
never published). `backend/.env.example`'s `KAFKA_BROKERS` default moved
from `localhost:9092` to `localhost:29092` to match — anyone still doing
local host-based dev (`go run` directly, per `backend/README.md`, still a
fully valid and probably more common workflow) needs to re-copy
`.env.example` or update their existing `backend/.env`.
`docker/docker-compose.yml`'s app services are unaffected — they already
used `KAFKA_BROKERS=kafka:9092`, which is exactly the INTERNAL listener's
now-correctly-advertised address. See `docker-compose.infra.yml`'s own
comment on the `kafka:` service for the blow-by-blow. RabbitMQ has no
advertised-address concept for AMQP (every client just dials
`<host>:5672` directly), so it needed no equivalent split.

Re-verified live after the fix: the exact same `createSkill` call that
had hung indefinitely returned in well under a second, and the full
cross-phase flow below passed cleanly on the first attempt afterward.

### SIGTERM vs. SIGINT: already handled, no code change needed

`docker compose down`/`docker stop` send `SIGTERM`, not `SIGINT` — a real
and common Docker-specific gotcha the plan flagged as worth checking
explicitly. Checked directly rather than assumed: `internal/platform/shutdown.Wait`
already calls `signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)`
(has, since Phase 1a — see that section's doc comment), and
notification-service's `app.enableShutdownHooks()` (NestJS) listens for
both signals by default too. No fix was needed here; this phase's
contribution is *confirming* it live through actual containers rather
than continuing to assume it from the source alone:

- `docker stop -t 15` against all 6 app containers simultaneously
  completed in under half a second total, all 6 exiting with code 0 (not
  a `docker stop` fallback to `SIGKILL`, which would have taken the full
  15s grace period and left a non-zero/signal-derived exit code).
- Each of the 4 Go services' logs showed the same
  `"shutdown signal received, starting graceful shutdown"` →
  `"shutdown complete"` pair Phase 1b–3.5's live verification already
  established, this time triggered by a container's `SIGTERM` rather
  than a terminal's `SIGINT`.
- notification-service logs no equivalent explicit line (NestJS's
  shutdown hooks aren't instrumented with an app-level log statement the
  way the Go side's `zap` calls are), so it was verified via external,
  observable state instead: the RabbitMQ management API showed
  `notifications.email`'s consumer count drop to `0` immediately after
  the container stopped (a clean channel close/consumer cancel, not an
  abruptly severed connection RabbitMQ would take longer to notice via
  heartbeat timeout).
- `jobs-service`'s three Kafka consumer groups
  (`jobs-service.user-skill-snapshot`/`.matching-worker`/
  `.cache-invalidator`) all showed `STATE: Empty`, `#MEMBERS: 0` via
  `kafka-consumer-groups.sh --describe --state` immediately after
  shutdown — a clean `LeaveGroup`, the same proof Phase 2's notes used
  for `SIGINT`, now confirmed for a container's `SIGTERM` instead.

### Why no app service has a Docker-level `HEALTHCHECK`

All 4 Go services' final image stage is `gcr.io/distroless/static:nonroot`
(no shell, no `curl`/`wget`, not even libc), so a `HEALTHCHECK` directive
has nothing to execute inside the container without teaching the binary a
new self-check mode — real application code, out of scope for a phase
whose brief is packaging what already exists, not adding to it. All 6 app
services remain externally checkable exactly as documented in each
service's own README (`/healthz`/`/readyz` over HTTP, `grpcurl` reflection
for the 4 Go services' gRPC surface) — see `docker/docker-compose.yml`'s
own comment for the fuller reasoning, including why `api-gateway`'s
`depends_on` against the other 4 Go services uses plain `service_started`
rather than `service_healthy` (there is no health status to gate on, and
every service already degrades gracefully when a peer isn't ready yet —
the same reasoning `depends_on: condition: service_healthy` against
Redis/Kafka/RabbitMQ exists to *reduce*, not eliminate, reliance on).

### New Makefile targets

`backend/Makefile` gained `up-full`/`down-full`/`nuke-full`, deliberately
separate from the existing `up`/`down`/`nuke` (infra-only) rather than
changing what those mean — local dev without Docker (`go run
./cmd/<service>` directly, against `make up`'s infra-only stack) is still
the faster, more common inner-loop workflow and shouldn't require a
Docker image rebuild on every iteration. `up-full`/`down-full`/`nuke-full`
build and run the full 6-service stack via
`docker compose --env-file ../docker/.env -f ../docker/docker-compose.infra.yml
-f ../docker/docker-compose.yml ...` — see the Makefile's own comment
above each target.

### Verification performed beyond `docker compose config`

`docker compose -f docker/docker-compose.infra.yml -f
docker/docker-compose.yml config` validated cleanly with no errors;
manual inspection of its output confirmed the two files merge into one
project sharing a single implicit default network (`docker_default`) —
`docker-compose.infra.yml` declares named volumes but no top-level
`networks:` block, so nothing conflicts — and that every `${...}`/service-name
substitution resolved as intended.

Brought up a throwaway `postgres:16-alpine` via a plain `docker run`
(outside these compose files, per the plan), ran `000_bootstrap.sql` then
all 5 services' goose migrations against it (via a disposable
`golang:1.27-alpine` container, since no local Go toolchain exists in
this environment — mirrors what every earlier phase's agent did),
gave each service role a known password, and pointed `docker/.env`'s
`*_DATABASE_URL` vars at it via `host.docker.internal` (Docker Desktop
resolves this automatically; this is exactly why the throwaway Postgres
stays outside the compose project rather than joining its network
directly — a container reaching a `docker run` container's *published*
host port is a different path than reaching another compose service by
name).

Brought the full stack up with `docker compose ... up -d --build` from a
genuinely clean state (`docker ps` confirmed empty before starting; every
container this session created was later torn down — see below). All 6
app services plus Redis/Kafka/RabbitMQ came up and reported
healthy/running (`/healthz` returned `{"status":"ok"}` on all 6 published
HTTP ports; Redis/Kafka/RabbitMQ's own Compose healthchecks, already
established since Phase 1a/2/3, all reported `healthy`).

**Reproduced the full cross-phase flow entirely through the containerized
stack** (not local `go run` processes) — the actual point of this phase:

- `register` → `login` → `createSkill` → `addUserSkill` → `createJob` →
  `myProfile`, all via `curl` against the containerized `api-gateway` at
  `localhost:8080/query` (see the Kafka-hang finding above — this is the
  flow that surfaced it, and the flow that then passed cleanly after the
  fix).
- **Kafka matching, live through containers:** after the fix, `addUserSkill`
  → jobs-service's snapshot consumer logged `"updated user skill
  snapshot"`; `createJob` requiring that skill → jobs-service's matching
  worker logged `"processed job.posted event"` with `matched_users: 1`;
  `job(id) { matches }` via GraphQL confirmed a `jobs.job_matches` row for
  the expected user with `score: 1`.
- **RabbitMQ welcome-email flow, live through containers:** confirmed via
  direct SQL that each `register` call produced exactly one
  `notifications.notification_log` row (`event_type: welcome`), matching
  `message_id`s between auth-service's producer and
  notification-service's consumer logs — the same cross-language proof
  Phase 3's notes established, now through containers on both sides.
- **RabbitMQ DLQ, live through containers:** published a deliberately
  malformed message (`{not-valid-json`) directly to `notifications.email`
  via the RabbitMQ management API; notification-service logged
  `"malformed notifications.email message; routing to DLQ"`, the message
  landed in `notifications.email.dlq` (queue depth 1, confirmed via the
  management API), and a subsequent real `register` call immediately
  after was still processed and inserted normally — the consumer survives
  a poison message and keeps consuming, through containers.
- **WebSocket `onNotification` push, live through containers:** rather
  than re-running Phase 3.5's full `-tags live` Go test suite (already
  merged and trusted; re-proving its correctness from first principles
  isn't this phase's job), a small standalone Python `graphql-transport-ws`
  client (stdlib `websockets`, no dependency on this codebase's own
  transport code) opened an authenticated subscription against the
  containerized gateway *before* `createJob`, then asserted a `next`
  message carrying `{"type":"job_match", ...}` arrived within 30s. It did,
  in under a second — the one thing this phase specifically needed to
  confirm (the containerized RabbitMQ→Redis pub/sub bridge still works),
  confirmed without re-deriving Phase 3.5's own correctness proof.
- **Graceful shutdown on `SIGTERM`, through containers** — see the
  dedicated section above.

**Torn down completely afterward, not left running**: `docker compose ...
down -v` (removed all 6 app containers, all 3 infra containers, all 3
named volumes, and the shared network), then the throwaway Postgres and
migration containers removed directly (`docker rm -f`). `docker ps -a`
and `docker network ls` confirmed nothing from this session remained.

## Phase 5 implementation notes

Local Kubernetes practice via `kind` — explicitly a sandbox/learning exercise (see the plan's Phase 5 bullet), not the production deploy path (that's Phase 7's single `allinone` binary on Render, a completely different and much simpler shape). Hand-written manifests under `k8s/base/<service>/` for all 6 app services + Redis, Strimzi for Kafka, the official RabbitMQ Cluster Operator for RabbitMQ, tied together by `k8s/overlays/kind/kustomization.yaml`.

### Environment reality check, reconfirmed live and tighter than assumed

The plan's environment section assumes ~16 GiB RAM. `docker info` at the start of this phase showed Docker Desktop's own VM — what `kind`'s node containers actually run inside — allocated only **~7.65 GiB**, not the host's full 16 GiB. This is the real, binding budget for everything in this phase, tighter than the plan's own framing. Every resource request/limit below was chosen with this number in mind, and the whole stack (kind's control-plane node, Strimzi + one Kafka broker, the RabbitMQ operator + one RabbitMQ pod, Redis, the throwaway Postgres, and all 6 app services) settled at **~2.26 GiB / 7.65 GiB (~30%)** resident once everything was up and idling, confirmed via `docker stats --no-stream` — comfortable headroom, not a near-miss.

### Cluster topology: single control-plane node, not control-plane + worker

`k8s/kind-cluster-config.yaml` defines one control-plane node only, a deliberate narrowing of the plan's "single node is fine, or control-plane + one worker if you want the extra practice, your call." A second node is pure kubelet/kube-proxy/kindnet overhead with zero scheduling benefit when every workload is pinned to `replicas: 1` anyway, and it wasn't worth the extra slice of the 7.65 GiB budget above.

### Resource requests/limits chosen, and why

- **Each of the 6 Go/NestJS app services:** 50m/64Mi (Go) or 50m/96Mi (NestJS) request, 300m/256Mi limit. NestJS gets a slightly higher memory request than the Go services — a Node runtime's baseline footprint (V8 heap, module graph) is meaningfully larger than a statically-linked Go binary's even doing less work.
- **Redis:** 25m/32Mi request, 150m/128Mi limit — a single-process cache with no clustering at this scale, the smallest footprint in the stack.
- **The throwaway Postgres:** 100m/128Mi request, 500m/384Mi limit.
- **Kafka (Strimzi `KafkaNodePool`):** 250m/768Mi request, 1 core/1200Mi limit, with `jvmOptions` pinning `-Xms`/`-Xmx` to 384m — a JVM left to size its own heap off the node's *visible* memory (which, on a shared kind node, looks larger than what this one pod is actually capped at) risks provisioning a heap the container's own cgroup limit then kills it for exceeding. No `entityOperator` block at all in the `Kafka` CR (not just left empty) — Strimzi's Topic Operator and User Operator are each an extra reconciler sidecar (~256Mi apiece) for `KafkaTopic`/`KafkaUser` CRs this project doesn't use (topics auto-create on first publish, exactly like `docker-compose.infra.yml`'s Kafka container); paying for two idle reconcilers here would be pure overhead.
- **RabbitMQ (`RabbitmqCluster`):** 250m/512Mi request, 1 core/512Mi limit — memory request **must equal** the limit for a RabbitMQ pod (see the gotcha below); everything else follows the same "small but not starved" sizing as the rest of the stack.
- **Strimzi's own cluster operator and the RabbitMQ Cluster Operator:** left at each project's own upstream defaults (a few hundred Mi apiece) — they're single, one-time-cost pods with no `replicas` knob this project's manifests control, and reducing them wasn't necessary to stay inside budget.

### The throwaway verification Postgres: genuinely not committed anywhere in this repo

Per this project's core architecture (one shared Supabase instance, schema-per-service — see the top of this file), there is no committed Postgres manifest anywhere in `k8s/`, exactly as `docker-compose.yml` never included one either. The Postgres actually used to verify this phase (`postgres:16-alpine`, a plain `Deployment` + `Service` with an `emptyDir` volume, `POSTGRES_PASSWORD` set to a throwaway value) was written directly into this session's scratchpad directory, **outside the repo entirely**, applied by hand (`kubectl apply -f`) against the `skill-bridge` namespace, and torn down along with the rest of the cluster at the end of this phase. `git status` before committing confirms nothing Postgres-shaped was ever staged. `migrations/000_bootstrap.sql` (run once, as the `postgres` admin role via `kubectl exec -i ... psql`) plus all 5 services' goose migrations (`make migrate-up`, port-forwarded to the throwaway Postgres, one admin `DATABASE_URL` covering every schema — exactly the pattern the Makefile's own comment already describes for a throwaway local Postgres) were run against it before any app pod started, then each service's own scoped role (`auth_service`, `users_service`, `skills_service`, `jobs_service`, `notification_service`) had its placeholder password replaced with a freshly generated one (`ALTER ROLE ... PASSWORD ...`) and that real value went only into that service's imperatively-created k8s `Secret` — never into `000_bootstrap.sql` itself, never into git.

### Secrets: committed `secret.example.yaml` shape, real values only via `kubectl create secret`

Same convention this project already uses for `.env.example` (see Secrets Management, above): every `k8s/base/<service>/secret.example.yaml` shows the expected keys with placeholder values and is deliberately **not** listed in `kustomization.yaml`'s `resources`, so `kubectl apply -k` never touches it. The real `Secret` objects (`DATABASE_URL`, `RABBITMQ_URL` where applicable, and `auth-service`'s `JWT_PRIVATE_KEY_PEM`) were created imperatively with `kubectl create secret generic ... --from-literal=... --from-file=...` for this session only. `JWT_PRIVATE_KEY_PEM` is a real RS256 keypair generated with `openssl genrsa` for this kind session specifically — not the ephemeral per-process key `auth-service` falls back to when the env var is unset, which is fine for a single local `go run` but would mean every pod restart invalidating every outstanding token, worth avoiding once a real `Secret` object is the natural place to put a real key.

### Real bugs/gotchas found live (this phase's genuine environment-specific surprises)

Same pattern as Phase 4's Kafka advertised-listeners gotcha — this phase turned up several, all versions-drifting-apart-from-each-other problems, not code bugs:

1. **The RabbitMQ Cluster Operator's official manifest requires cert-manager**, silently, for its admission webhooks — `kubectl apply`ing it produces three `no matches for kind "Certificate"/"Issuer"` errors (cert-manager's CRDs aren't installed) and the operator pod itself then sits in `ContainerCreating` forever with a `FailedMount` event, because its Deployment mounts a Secret (`cluster-operator-webhook-server-cert`) that a cert-manager `Certificate` resource was supposed to create and never did. Rather than add cert-manager as a whole extra operator for a learning sandbox that doesn't need admission-webhook validation, this phase deleted the operator's `MutatingWebhookConfiguration`/`ValidatingWebhookConfiguration` and hand-generated a self-signed cert (`openssl req -x509`) loaded into the two Secrets (`cluster-operator-webhook-server-cert`, `rabbitmq-cluster-operator-metrics-server-cert`) the Deployment expects, purely so the container can start its internal HTTPS listener — no external caller ever validates this cert's chain now that the webhook registrations are gone.
2. **Deleting that mutating webhook has a real consequence**, not a free lunch: it was also responsible for defaulting `RabbitmqCluster.spec.image`. Without it, an unset `image` field reaches the generated `StatefulSet` as a required-but-empty value and the whole `StatefulSet` is rejected (`spec.template.spec.containers[0].image: Required value`) — so `k8s/rabbitmq/rabbitmq-cluster.yaml` sets `spec.image` explicitly (`rabbitmq:4.1-management`), which it would not otherwise have needed to.
3. **RabbitMQ pod memory request must equal its limit**, or the `RabbitmqCluster` surfaces a `MemoryRequestAndLimitDifferent` warning condition — Erlang's VM wants to know its true memory ceiling up front for its own alarm-watermark calculation, unlike a Go/JVM process that tolerates `request < limit` fine. Fixed by setting both to `512Mi`.
4. **Strimzi's Kafka CRD moved from `kafka.strimzi.io/v1beta2` to `kafka.strimzi.io/v1`** between whatever version this plan was originally written against and the version `https://strimzi.io/install/latest?namespace=kafka` actually installed live in this session (`quay.io/strimzi/operator:1.2.0`) — applying with `v1beta2` fails with a plain "no matches for kind" error. `k8s/strimzi/kafka-cluster.yaml` uses `v1` throughout.
5. **That same Strimzi build no longer supports Kafka 3.9.0** (`docker-compose.infra.yml`'s pinned version) — the `Kafka` CR's own status condition names the supported set outright (`UnsupportedKafkaVersionException: ... Supported versions are: [4.2.0, 4.2.1, 4.3.0, 4.3.1]`), so this phase's manifest uses `4.3.1` / `metadataVersion: 4.3-IV0` instead. Per this file's existing "docker-compose and k8s manifests don't need to stay in perfect sync" note, this divergence is fine as-is rather than something to reconcile backward.
6. **The RabbitMQ Cluster Operator's default `startupProbe` doesn't work on any RabbitMQ version tried** (3.13, 4.0, and 4.1 management images all confirmed live, via `kubectl logs`/`kubectl exec ... rabbitmq-diagnostics --help`, to be fully up and correctly serving the management HTTP API — this wasn't a slow-boot symptom): it `GET`s `/api/health/checks/reached-target-cluster-size`, which 404s on all three, and `rabbitmq-diagnostics --help` doesn't list any check by that name on any of them either. Fixed via `RabbitmqCluster.spec.override.statefulSet` to replace the container's `startupProbe` with an exec check (`rabbitmq-diagnostics check_running`) — version-stable, and semantically correct for a single-node, non-HA cluster where "is RabbitMQ itself up" is the whole of what readiness needs to mean.
7. **Six app `Deployments` need `RABBITMQ_URL` as one connection-string env var, but the operator's default behavior is to auto-generate a random per-instance username/password into a Secret** (`<name>-default-user`) with separate `username`/`password`/`host`/`port` keys — Kubernetes env vars can't be composed from multiple `secretKeyRef`s (no string concatenation), and this project's distroless app images have no shell to do that composition in an entrypoint script either. Worked around by pinning a known `default_user`/`default_pass` directly in `RabbitmqCluster.spec.rabbitmq.additionalConfig` and mirroring those same credentials into each service's own `Secret` by hand — a real, working trade-off for a throwaway sandbox, explicitly not what a production `RabbitmqCluster` should do (that would want the generated-secret + projected-volume pattern instead, which this phase's app manifests don't build).

None of the above are Go/NestJS application code bugs — every one is a version/tooling mismatch between "whatever's newest right now" (Strimzi's `latest` install URL, the RabbitMQ Cluster Operator's latest GitHub release) and either this plan's original assumptions or this project's own already-pinned `docker-compose.infra.yml` versions. Consistent with this project's per-phase pattern of turning up exactly one or two environment-shaped surprises (Phase 4's Kafka advertised-listeners issue being the most recent before this one).

### Verification performed

Brought up `kind create cluster --name skill-bridge --config k8s/kind-cluster-config.yaml`, installed Strimzi (`kubectl create namespace kafka && kubectl apply -f 'https://strimzi.io/install/latest?namespace=kafka' -n kafka`) and the RabbitMQ Cluster Operator (`kubectl apply -f ".../cluster-operator.yml"`) and waited for both to be `Available`, applied `k8s/strimzi/kafka-cluster.yaml` and (with a real password substituted for the committed placeholder) `k8s/rabbitmq/rabbitmq-cluster.yaml`, applied the throwaway Postgres, ran `000_bootstrap.sql` + `make migrate-up` against it, built and `kind load docker-image`'d all 6 service images (`backend/Dockerfile.<service>` and `backend/notification-service/Dockerfile`, unmodified), created each service's real `Secret`, and applied `kubectl apply -k k8s/overlays/kind`.

- **Every one of the 6 app pods, Redis, Kafka, RabbitMQ, and the throwaway Postgres reached `1/1 Running` on the first try** after the fixes above, with **zero restarts** observed over a multi-minute window (`kubectl get pods -n skill-bridge`/`-n kafka`/`-n rabbitmq` re-checked ~90 seconds apart, restart counts unchanged at 0 throughout) — not a "worked once then crash-looped" result.
- **The smoke test, live, through `kubectl port-forward svc/api-gateway 18080:8080` against the actual k8s-deployed pods** (a plain `curl`-based GraphQL check, per this phase's efficiency guidance — the heavier hand-rolled WebSocket protocol client stays where Phase 3.5 already proved it): `{ ping }` → `register` → `login` → `createSkill` → `createJob` (with `requiredSkills` resolving through skills-service) → `myProfile` (JWT verified against auth-service's live JWKS endpoint, reached over the `auth-service.skill-bridge.svc.cluster.local` in-cluster DNS name) → `addUserSkill`, all succeeded with the expected shapes.
- **One Kafka-matching check:** `addUserSkill` (→ `user.skills.updated` on the real Strimzi-managed broker) followed by a second `createJob` requiring that same skill produced a `job_matches` row within ~3 seconds, confirmed both through the GraphQL `job(id: ...) { matches }` query (`score: 1`) and directly via SQL (`select * from jobs.job_matches`) — driven entirely by Kafka events consumed by the k8s-deployed `jobs-service` pod, no direct DB or RPC shortcut.
- **One RabbitMQ-email check:** `register`'s `notifications.email` publish was confirmed consumed by the k8s-deployed `notification-service` pod via its own logs (`"would send welcome email to ..."`, `"result":"inserted"`) and independently via SQL (`select * from notifications.notification_log`) showing the row.
- A harmless, already-documented startup race was observed and not treated as a bug: api-gateway logged `failed to fetch auth-service JWKS at startup; will retry lazily on first use` because it started fractionally before auth-service's HTTP listener was up — exactly the degrade-and-retry behavior this codebase already implements, and `myProfile`'s later JWT verification succeeded once auth-service was reachable.
- Resource usage stayed well within budget throughout: `docker stats --no-stream` on the kind node container showed ~2.26 GiB / 7.65 GiB (~30%) at steady state with the full stack running.
- Teardown: `kind delete cluster --name skill-bridge`, confirmed via `kind get clusters` (no clusters listed) and `docker ps` (no `skill-bridge-control-plane` container remaining) — this phase's resources cannot coexist with Phase 6's on this machine's budget, and Phase 6 needs a clean slate.

### Deferred / not built this phase

- **No ingress/ext port mapping** — verification used `kubectl port-forward` directly, per the plan; `k8s/kind-cluster-config.yaml` has no `extraPortMappings`.
- **No `NetworkPolicy` objects** — every namespace can reach every other by default on kind's CNI, consistent with this project's existing "plain private-network trust, no service mesh/mTLS" stance (see Scope Cuts) — nothing new introduced for k8s specifically.
- **The RabbitMQ webhook workaround (self-signed cert, deleted webhook configs) is sandbox-only** — a real cluster would install cert-manager (or an equivalent) rather than hand-generate certs and disable admission validation.

## Phase 6 implementation notes

Local Jenkins practice via `jenkins/docker-compose.jenkins.yml` — an explicitly build-only sandbox (checkout → test → docker build, no deploy stage) that gates nothing real; GitHub Actions remains the only pipeline an actual merge depends on. See `jenkins/README.md` for the day-to-day usage and the three design choices (container-per-stage, `file://` SCM off the bind-mounted bare repo, JCasC + Job DSL job provisioning) already written up there.

### Real bugs found live, all environment/tooling drift rather than Jenkinsfile logic — consistent with every prior phase's pattern

1. **JCasC schema mismatch crash-looped the controller on every boot.** The original `authorizationStrategy: loggedInUsersCanDoAnything: { anonymousRead: true }` failed with `UnknownAttributesException: ... anonymousRead` — that attribute isn't valid on this Jenkins/plugin version's `FullControlOnceLoggedInAuthorizationStrategy` JCasC schema. A first fix attempt (`loggedInUsersCanDoAnything: true`, a bare boolean) failed differently (`Item isn't a Mapping` — JCasC requires a mapping value there even when empty). The container was crash-looping (Docker's restart policy retrying forever, exit code 5 each time), not merely slow to boot — worth stating plainly since from the outside "Jenkins is taking a long time" and "Jenkins is crash-looping" look identical until you actually check `docker ps` for a `Restarting` status and read past the tail of accumulated shutdown-stack-trace log lines to find the actual `SEVERE` line further back. Final state that boots cleanly and preserves the original anonymous-read intent: `loggedInUsersCanDoAnything: {}` as the mapping shape. One subtlety worth naming explicitly: a `docker restart` of an already-working container proves nothing about a *committed* fix, since the image's baked-in config is frozen at build time regardless of later host-file edits — the file was re-verified with `docker rmi` + `docker compose build --no-cache` + a fresh `up` from zero cached layers, confirming the exact committed content boots clean, serves anonymous reads (`curl` with no credentials against `/api/json` returns 200), and auto-provisions the pipeline job on a genuinely clean checkout, not just in an already-running container.
2. **golangci-lint's official `curl | sh` installer failed its own checksum verification** against the actual `v2.13.1` linux/arm64 release tarball from GitHub (reproduced twice, not a corrupted single download) — a real mismatch in the installer's embedded checksum manifest, not an environment problem on this machine's end. Fixed by switching to `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}` instead, which verifies via the Go module checksum database (a different, working trust path) rather than the installer script's bundled manifest.
3. **The Jenkinsfile's own pipeline definition doesn't live-reload per build.** `casc/jobs/seed.groovy` reads `jenkins/Jenkinsfile`'s text once, at Jenkins startup (from a plain bind mount, not the bare-repo clone the actual Checkout stage uses), and bakes it into the job's Job DSL-provisioned `cps` definition. Editing `jenkins/Jenkinsfile` on the host and re-triggering a build without restarting the container re-runs the *old* pipeline text — confirmed directly: a build triggered after fixing the golangci-lint installer still hit the original checksum failure, because the running container had booted before that edit. Restarting the container (`docker restart sbp-jenkins`) picks up the current file on next boot. This is a deliberate trade-off stated in `seed.groovy`'s own comment, not treated as a bug to engineer around (e.g. with "pipeline from SCM" reading the Jenkinsfile fresh per-build) — acceptable for a sandbox that's brought up and torn down on demand, not left running for weeks between edits.
4. **A build interrupted by a mid-flight Jenkins restart fails with a misleading error**, not because of anything wrong in the pipeline: `docker.image(...).inside{}` containers don't survive the controller restarting, so a resumed build step logs `process apparently never started` against a container that no longer exists. Not a real failure mode to fix — just something to recognize rather than chase as a pipeline bug if a build happens to fail exactly when the controller was restarted underneath it. The fix is procedural: don't restart the Jenkins container while a build is in flight, and re-trigger a fresh build once the controller is stable.

### Verification performed

Brought up Jenkins fresh (`docker compose -f jenkins/docker-compose.jenkins.yml up -d --build`), confirmed via its own logs (`"Jenkins is fully up and running"`) and the REST API (`/api/json` showing the `skill-bridge-backend-practice` job, Job DSL-provisioned with zero manual clicking) that it booted cleanly after the JCasC fix above. Triggered a build via the REST API (crumb-issuer + `POST .../build`, both documented in `jenkins/README.md`) rather than clicking through the UI, since this is meant to be scriptable.

**One clean, uninterrupted run (build #6) succeeded end to end**: Checkout (cloned from the bind-mounted bare repo via `file://`, correct branch, correct commit), Test (`go build/vet/test` all passed inside a `golang:1.27` container; `golangci-lint run ./...` and `buf lint` both passed after the installer fix; `npm ci && npm run lint && npm test` passed inside a `node:22-alpine` container for `backend/notification-service`), and Build (all 6 app images — `skill-bridge/{auth,skills,users,jobs}-service`, `skill-bridge/api-gateway`, `skill-bridge/notification-service`, each tagged `:jenkins` — built successfully via the Docker Pipeline plugin's socket-mounted Docker access, no push anywhere). Confirmed via `docker images` that all 6 `:jenkins`-tagged images exist.

Per this project's own efficiency guidance (root `CLAUDE.md`): the Build stage's own logic legitimately builds all 6 images every real run (that's its actual job), but *verifying the pipeline mechanism works at all* didn't need to watch all 6 complete — the first successful `docker build` inside the pipeline already proved Docker-in-Docker-via-socket-mount works, since every image follows an identical pattern already confirmed working outside Jenkins back in Phase 4. Letting the already-in-flight build finish anyway (rather than interrupting it) cost wall-clock time, not additional orchestration effort.

Torn down completely afterward (`docker compose -f jenkins/docker-compose.jenkins.yml down -v` — removes the controller container and its named volume): confirmed via `docker ps -a` that nothing Jenkins-related remained.

### Deferred / not built this phase

- No GitHub webhook, no SCM polling trigger — builds are manual/on-demand only, by design (this Jenkins is never exposed beyond `localhost`).
- No image push anywhere — no registry configured, nowhere to push to.
- No deploy-to-`kind` stage — already exercised by hand in Phase 5; re-adding it here would duplicate that work for no new learning value.

## Phase 7 implementation notes

The final phase: `backend/cmd/allinone` (the single binary the "Production deployment" section above already committed to before this phase built it), `backend/Dockerfile.allinone`, `render.yaml`, `frontend/vercel.json`/`frontend/.env.example`, and CORS on api-gateway. Everything **short of** actually creating a Supabase project, a Render account, and a Vercel account and connecting them — that boundary is explicit in this phase's own brief and is spelled out precisely in the "What genuinely cannot be done here" section below. Concrete choices made while building this slice:

### `cmd/allinone`: assembly, not new business logic

Every `NewServer` call (`auth.NewServer`, `skills.NewServer`, `users.NewServer`, `jobs.NewServer`) and the gateway's resolver/gRPC-client wiring in `cmd/allinone/main.go` is copied unchanged from each service's own `cmd/<service>/main.go` — this file's whole job is registering four backend gRPC servers on fixed loopback addresses (`127.0.0.1:9001`-`9004` for gRPC, `127.0.0.1:8081`-`8084` for each service's own health/JWKS HTTP mux) plus api-gateway's public HTTP/GraphQL/WebSocket server (`0.0.0.0:$PORT`, Render's env var convention) in one process. See `cmd/allinone/README.md` and the package doc comment at the top of `main.go` for the full reasoning; the two genuinely new pieces:

- **`dbGetenv`/`connectDB`**: four services still need four distinct scoped-role Postgres connection strings despite sharing one process-wide environment. `db.LoadConfig` (`internal/platform/db`) already takes an injectable `getenv` function for exactly this reason (it existed since Phase 1a for testability — Phase 7 is the first caller that actually needs the injection for its intended purpose rather than just a test double). `dbGetenv(prefix)` maps the single key `"DATABASE_URL"` to `"<prefix>_DATABASE_URL"` and passes everything else through unchanged, reusing the `AUTH_DATABASE_URL`/`SKILLS_DATABASE_URL`/`USERS_DATABASE_URL`/`JOBS_DATABASE_URL` naming `docker/docker-compose.yml` already established in Phase 4 rather than inventing a fifth convention.
- **`shutdown.Options.GRPCServers`** (plural, new field in `internal/platform/shutdown`): every prior `cmd/<service>/main.go` has exactly one `*grpc.Server`, so `Options.GRPCServer` (singular) was sufficient until now. Added `GRPCServers []*grpc.Server` alongside it (both may be set; every non-nil server across the two gets `GracefulStop`ed **concurrently**, not sequentially, so one slow server can't eat the whole shutdown timeout budget before the others even start draining) — purely additive, every existing single-server caller is unaffected. `internal/platform/shutdown/shutdown_test.go`'s `TestWait_StopsMultipleGRPCServers` proves this with four real `grpc.Server`s each serving the standard health service: before `Wait` is triggered all four answer a health check; after it returns, all four connections are refused — not just the first, which a naive single-field implementation would have left running.

Two small pieces of gateway logic (`wsInitFunc`'s WebSocket `connection_init` auth, `isTransportError`) are **duplicated** verbatim from `cmd/api-gateway/main.go` into `cmd/allinone/main.go` rather than extracted into a shared package. These are unexported helpers in an independent `package main`, so importing them isn't an option across two binaries; extracting them would mean touching `cmd/api-gateway/main.go`'s already-live-tested WebSocket auth code (Phase 3.5) and updating the half-dozen doc comments elsewhere in the codebase that point at "`cmd/api-gateway/main.go`'s `wsInitFunc`" as a reference — judged not worth the churn/risk for roughly 40 lines of stable, unlikely-to-change logic. Both copies carry a comment noting they must be kept in sync if the gateway's WebSocket-auth logic ever changes.

### What genuinely doesn't run in production, and why that's by design, not a bug

Per the plan and the "Production deployment" section above, Kafka/RabbitMQ async flows don't exist in this deployment. `cmd/allinone` still constructs every Kafka producer/consumer and RabbitMQ producer/consumer exactly as each original service does — nothing here special-cases "production" in code. The mechanism is entirely `render.yaml` simply never setting `KAFKA_BROKERS`/`RABBITMQ_URL`, combined with the degrade-gracefully behavior every one of those clients already had since Phase 2/3. The consequence, stated precisely so it isn't discovered by surprise later:

- **`jobs.job_matches` is never populated.** Nothing publishes or consumes `job.posted`/`user.skills.updated` without Kafka, so the matching worker and the `user_skill_snapshot` projection simply never run. `createJob`/`addUserSkill` still succeed (they don't depend on Kafka to write their own tables) — `Job.matches` just always resolves empty.
- **No welcome-email log line is ever recorded** (`notifications.notification_log` stays empty) — `Register`'s RabbitMQ publish is a best-effort no-op with `RABBITMQ_URL` unset, same as it's always been when RabbitMQ is down.
- **`onNotification` never receives a live push** — the WebSocket subscription still connects and authenticates correctly (that part has no broker dependency), it just never has anything to forward.
- **The rest of the synchronous GraphQL surface is unaffected**: `register`, `login`, `googleAuthUrl`/`googleOAuthCallback`, `createSkill`, `createJob`, `myProfile`/`updateProfile`/`addUserSkill`, and every query all work identically to the full microservices deployment, since none of them depend on a broker to do their own job.

### Redis: optional, not provisioned by this Blueprint, both states verified live

The plan's Phase 7 bullet calls for rate limiting and pub/sub to "degrade gracefully to in-process behavior when `REDIS_URL` is unset" — this was already true since Phase 1c/3.5 (`internal/platform/cache.NewFromEnv` degrades every method to a safe no-op/miss), so Phase 7's job was confirming it actually holds for `cmd/allinone` specifically, not building new degrade logic. `render.yaml` leaves `REDIS_URL` unset by default (`sync: false`, no value) — a free managed Redis with pub/sub support isn't guaranteed on Render's free tier (Render's own Redis is paid-only), and this project isn't provisioning a third-party one as part of this Blueprint. Verified live, both ways, against the actual `allinone` binary (not just unit tests):

- **`REDIS_URL` unset**: the binary starts cleanly, logs `"REDIS_URL not set; caching and rate limiting are disabled on this instance"` once per client construction (three separate `cache.Client` instances — skills-service's cache, jobs-service's cache, the gateway's rate-limit/pub-sub client — mirroring the three separate connections the full microservices deployment already has), and every subsequent request through `ratelimit.Middleware` logs `"rate limiter unavailable (redis unreachable/disabled); allowing request through"` and lets the request proceed. Fired 5 rapid identical requests; all 5 returned `200`.
- **`REDIS_URL` set** (a local Redis, aggressive `RATE_LIMIT_REQUESTS=3`/`RATE_LIMIT_WINDOW_SECONDS=8` for a fast test): the first 3 requests within the window returned `200`; the 4th and 5th returned `429` with the documented JSON body — confirming the exact same binary genuinely *enforces* the limit once Redis is actually reachable, not just that it fails open when it isn't. If a free-tier external Redis (e.g. Upstash) is ever wired into the real Render deployment, every one of caching/rate-limiting/realtime-notification-fan-out activates with zero code change.

### CORS: an explicit allowlist, not `*`, `internal/gateway/cors`

`docs/SECURITY.md` tracked "no CORS configuration exists yet" as an open gap since the frontend never called the backend before this phase. `internal/gateway/cors.Middleware(allowedOrigins, log)` wraps the whole HTTP mux on both `cmd/api-gateway/main.go` and `cmd/allinone/main.go` (not just `/query`) — cheap to apply broadly since it's a no-op for any request with no `Origin` header (same-origin navigation, `curl`, `grpcurl`-style debugging, server-to-server calls all pass through unaffected).

- **`CORS_ALLOWED_ORIGINS`** (comma-separated, `internal/gateway/cors.LoadAllowedOriginsFromEnv`) defaults to `http://localhost:5173` (Vite's local dev origin) when unset. Deliberately never `*`: this project's auth is a bearer token in an `Authorization` header, not a cookie, so a wildcard origin wouldn't itself leak a session the way it could for cookie-based auth — but defaulting to `*` still normalizes "any origin may call this API," the wrong default to carry into a real deployment.
- **Only the matching origin is ever echoed back** in `Access-Control-Allow-Origin` (never `*`), with `Vary: Origin` always set so a cache in front of this can't serve one origin's response to another. A non-matching or missing origin gets no CORS headers at all and the request still reaches the handler unrejected — enforcement is the browser's own same-origin policy blocking the calling page's JS from reading the response, not a server-side 403; this middleware's job is only to decide which responses are readable cross-origin, never to gate access to the API itself (that's still authctx/ratelimit's job, unaffected by this).
- **Verified live** against the running `allinone` binary: a request with `Origin: https://allowed-test-origin.example.com` (present in `CORS_ALLOWED_ORIGINS`) got `Access-Control-Allow-Origin: https://allowed-test-origin.example.com` back; the identical request with `Origin: https://not-allowed.example.com` got no `Access-Control-Allow-*` headers at all, both against `/healthz`. `internal/gateway/cors/cors_test.go` additionally covers the preflight `OPTIONS` short-circuit and the no-`Origin`-header pass-through case.

### `render.yaml`: repo root, `rootDir: backend`

Render's Blueprint feature looks for `render.yaml` at the repository root by default when a GitHub repo is connected as a Blueprint — placing it in `backend/` instead would mean Render never finds it without an extra manual "Blueprint file path" configuration step. `rootDir: backend` on the one `web` service is what points its Docker build context (`dockerfilePath: ./Dockerfile.allinone`, relative to `rootDir`) at the right subdirectory of this monorepo without `render.yaml` itself needing to live there. `frontend/`'s Vercel connection is entirely separate (a different platform, a different project) — this file only describes the one Render service.

Every real secret (`AUTH_DATABASE_URL`/`SKILLS_DATABASE_URL`/`USERS_DATABASE_URL`/`JOBS_DATABASE_URL`, `JWT_PRIVATE_KEY_PEM`, the Google OAuth trio, `REDIS_URL`, `CORS_ALLOWED_ORIGINS`) is `sync: false` — Render prompts for a real value in its dashboard after the Blueprint is applied, and nothing here is ever committed with one. `KAFKA_BROKERS`/`RABBITMQ_URL` are absent entirely, not merely commented out, matching the "these simply don't exist in production" decision above — omission is the actual mechanism, not a flag.

### `frontend/vercel.json` and `frontend/.env.example`

Vite is genuinely zero-config on Vercel once a project's Root Directory is set to `frontend` in Vercel's own project settings (a dashboard setting, not something `vercel.json` replaces for a monorepo) — Vercel's build/output detection would work without any `vercel.json` at all. It's included anyway for one concrete, non-speculative reason: `frontend`'s `react-router-dom` dependency means any client-side route beyond `/` (once routes exist) needs a SPA fallback rewrite to `index.html`, or a direct navigation/refresh on a non-root path 404s — a real, common Vercel/SPA gotcha worth pinning explicitly now (`rewrites: [{ source: "/(.*)", destination: "/index.html" }]`) rather than discovering it the first time a route is added. `framework: "vite"`/`buildCommand`/`outputDirectory` are pinned explicitly too, matching what Vercel would auto-detect, so the config is self-documenting rather than implicit.

`frontend/.env.example` adds `VITE_API_URL`, defaulting to `http://localhost:8080/query` (api-gateway's local dev default) with a commented placeholder for the eventual Render URL (`https://CHANGE_ME.onrender.com/query`) — nothing in `frontend/src/` reads this yet (per this phase's explicit scope boundary: no real frontend↔backend integration is being built), it's scaffolding so a real deploy has somewhere to point once that integration exists.

### Verification performed beyond `go build/vet/test`

- **`go build ./...`, `go vet ./...`, `go test ./...`, and `golangci-lint run ./...`** all clean from `backend/`, including the new `cmd/allinone` and `internal/gateway/cors` packages and the `internal/platform/shutdown` change. `buf lint` also re-run (no `.proto` changed this phase, so no diff expected or found).
- **Environment note**: this session had no local Go toolchain on `PATH` by default (found at `/opt/homebrew/bin/go`, a `PATH` issue not a missing-install issue) and no working Docker daemon at the start of the phase (`Docker.app` present but not a functional installation). `colima` + the `docker` CLI were installed via `brew` specifically to satisfy this phase's "build `Dockerfile.allinone` successfully" requirement with a real Docker daemon — noted here since every prior phase's notes describe using Docker Desktop directly; this phase used Colima's Lima-VM-backed daemon instead, which behaves identically for `docker build`/`docker run` purposes (confirmed below) but is a genuine environment difference worth recording rather than silently glossing over.
- **`Dockerfile.allinone` built successfully**: `docker build -f backend/Dockerfile.allinone backend` completed with no errors, producing a working image.
- **The core live proof, run twice** (once as a plain `go build` binary, once as the built Docker image, to prove both the code and the packaging): a throwaway local Postgres — a native Homebrew `postgresql@16` instance on a non-default port, since no Docker was available yet at the point migrations were first run; functionally identical to every prior phase's `docker run postgres` pattern — had `migrations/000_bootstrap.sql` and all four services' goose migrations (`auth`/`users`/`skills`/`jobs`, each with its own schema-qualified tracking table per the Phase 1b gotcha) applied against it, then `cmd/allinone` was started with `REDIS_URL`/`KAFKA_BROKERS`/`RABBITMQ_URL` all **deliberately unset** and `AUTH_DATABASE_URL`/`SKILLS_DATABASE_URL`/`USERS_DATABASE_URL`/`JOBS_DATABASE_URL` pointing at that Postgres (via `host.docker.internal` for the containerized run, matching the exact mechanism `docs/DECISIONS.md`'s Phase 4 notes already used for the same reason).
  - The process started without crashing, logged every degrade-gracefully warning expected (Redis, Kafka ×4 producers, RabbitMQ ×2 producers + the gateway's realtime consumer, all three Kafka consumers skipped) and nothing else.
  - `GET /healthz` and `/readyz` both returned `200 {"status":"ok"}` on the single public port.
  - **The full `register` → `login` → `createSkill` → `createJob` → `myProfile` flow succeeded end-to-end through this one process on one port** — `createJob`'s `requiredSkills` resolved correctly (proving the gateway's in-process loopback dial to skills-service works), and `myProfile` succeeded with a valid bearer token and was rejected with a GraphQL (not transport-level) error with no token — both confirmed in both the binary and the container run.
  - Rate limiting's fail-open path (5 rapid requests, all `200`, each logging the fail-open message) and its actually-enforcing path (a second run with a real local Redis and an aggressive 3-req/8s limit: first 3 `200`, next 2 `429`) were both confirmed against the running binary — see the Redis section above.
  - CORS was confirmed against the running binary — see the CORS section above.
  - **Graceful shutdown**: `SIGTERM` to the running binary produced `"shutdown signal received, starting graceful shutdown"` immediately followed by `"shutdown complete"` (all four gRPC servers stopped concurrently via the new `GRPCServers` field, all five HTTP servers — the public gateway one plus each service's own loopback health/JWKS one — shut down, no broker consumers to wait on since none were configured), and the process fully exited with no zombie (confirmed via `pgrep`/`lsof` showing nothing on any of the nine ports afterward).
- **Teardown**: the test container was removed (`docker rm -f`), the local Redis instance used for the enforcing-rate-limit test was shut down, and the throwaway Postgres instance was stopped after this phase's verification concluded. Colima/Docker were left installed (a genuine, reusable environment fix rather than session-scoped scaffolding) but the VM was stopped, matching this project's established "tear down before moving on" convention for anything resource-heavy.

### What genuinely cannot be done here (requires the user's own account creation)

Everything below needs a live external account/credential this environment has no way to create or fake, per this phase's explicit scope boundary:

1. **Create a Supabase project** (the plan's Phase 0 step, still not done — every phase through Phase 6 also ran against a throwaway local Postgres for this exact reason) and run `migrations/000_bootstrap.sql` against it with a real admin connection, then set real per-role passwords.
2. **Create a Render account**, connect this GitHub repo, and apply `render.yaml` as a Blueprint. Fill in every `sync: false` env var it lists with the real Supabase Supavisor connection strings (one per service role), a real `JWT_PRIVATE_KEY_PEM` (e.g. `openssl genrsa 2048`, reformatted to one line with literal `\n`), and optionally the Google OAuth trio and `REDIS_URL` if desired.
3. **Create a Vercel account**, import this repo, set its project's Root Directory to `frontend`, and set `VITE_API_URL` to the real `https://<render-service>.onrender.com/query` once step 2 produces that URL.
4. **Come back and set `CORS_ALLOWED_ORIGINS`** on the Render service to the real Vercel URL from step 3 (a circular dependency between steps 2 and 3 that's unavoidable — the Render URL isn't known until the service exists, and the Vercel URL isn't known until *its* deploy exists — resolved by doing an initial deploy of each, then a follow-up env var update on Render once both URLs are known).
5. **Create a Google Cloud OAuth client** (Web application credentials, a stable redirect domain — not a Vercel preview URL, per the plan's Phase 0 note) if Google login is wanted; set `GOOGLE_OAUTH_REDIRECT_URL` to the deployed frontend's callback route.

None of the above can be stubbed against a placeholder the way `DATABASE_URL`/`GOOGLE_OAUTH_*` were stubbed against local dev in earlier phases — a Supabase project, a Render service, and a Vercel project are all real accounts on real third-party platforms that only the repo owner can create.

## Frontend auth wiring (post-Phase 7, not part of the original plan)

The original plan never scoped connecting `frontend/` to the backend — every phase through 7 focused entirely on the backend, and the frontend stayed the unwired Vite scaffold Phase 1a's `git mv` left it as. This closes that gap for auth specifically (register, login, viewing your own profile), deliberately scoped smaller than the full feature set (skills/jobs/matching/realtime) to get one verified, working pattern in place before repeating it across more pages — see `frontend/README.md` for what exists.

**GraphQL client: a ~20-line `fetch` wrapper, not Apollo/urql/`graphql-request`.** Three operations (`register`, `login`, `myProfile`) don't justify a client whose main value is caching/normalization across a large query surface; TanStack Query already covers the request-state (loading/error/retry) half of what a full GraphQL client would add. Revisit once skills/jobs are wired up and the query surface actually grows.

**Auth storage: Zustand + `persist` to `localStorage`, not an httpOnly cookie.** The backend has no session/cookie mechanism at all — `AuthPayload` is `{ accessToken, userId }`, a bearer JWT, full stop. A cookie-based approach would require the gateway to *set* a cookie, which it doesn't do and isn't in scope here; `localStorage` is what the actual API surface supports today, matching the plan's original Phase 1a note that this exact tradeoff (httpOnly cookie vs. localStorage) was deferred, not decided.

**A real bug this surfaced, not introduced by the frontend work**: `login`'s `AuthPayload.userId` always comes back `""` — confirmed live (see Verification below), and confirmed deliberate by `schema.resolvers.go`'s `Login` resolver comment: auth-service's `Login` RPC only ever returns a token, and the gateway's resolver documents that "the client can decode the JWT's `sub` claim locally if it needs the ID without a second round trip" rather than adding a second lookup. The frontend does exactly that (`src/lib/jwt.ts`'s `decodeJwtSubject`, applied uniformly to both `register` and `login` for one consistent code path) rather than treating the empty string as a bug to fix on the backend — changing `Login`'s wire shape now would be schema churn for behavior that's already documented as intentional.

### Verification performed

A throwaway local Postgres (`docker run postgres:16-alpine`) was bootstrapped and migrated (`000_bootstrap.sql` + `make migrate-up`), then `auth-service`, `users-service`, and `api-gateway` were run directly (`go run`, no Docker) with `REDIS_URL`/`KAFKA_BROKERS`/`RABBITMQ_URL` all deliberately unset, degrading gracefully as expected. Against that live gateway:

- `register` → real `accessToken` + `userId` returned; `login` with the same credentials → real `accessToken`, empty `userId` as described above (confirming the bug is real and reproducible, not assumed from reading the resolver).
- `myProfile` with the `register` token → succeeded, returning the correct `userId` (recovered from the JWT `sub` claim, not the empty field) with an empty profile shell (lazily created, matching earlier phases' documented behavior).
- `myProfile` with no `Authorization` header → a GraphQL-level `"authentication required"` error, not a transport-level failure.
- **CORS preflight and the actual request were both exercised with `Origin: http://localhost:5173`** (the frontend's real dev origin, not just localhost with no origin) — both correctly returned `Access-Control-Allow-Origin: http://localhost:5173`, confirming the browser-facing mechanism the frontend actually depends on, not just that the gateway responds at all.
- `npm run build` (`tsc -b && vite build`) and `npm run lint` both clean.
- The throwaway Postgres, the three `go run` processes, and the Vite dev server were all torn down after verification — nothing was left running.

Not done: an actual browser click-through (this environment has no interactive browser available) — the above is the closest available substitute (the exact GraphQL operations the frontend's code sends, plus a CORS check that mimics what a real `fetch` from the SPA's origin would do).

## Frontend feature completion: skills, jobs, matching, realtime notifications

Follow-on to the auth-only wiring above, closing the rest of the gap — skill browsing/creation, job posting/browsing, viewing a job's matches, adding a skill to your own profile, and a live notification bell. Google OAuth and `updateProfile` are the two remaining API-only flows (see `frontend/README.md`'s Known gaps) — deliberately deferred rather than an oversight, since neither blocks demonstrating the rest of the architecture end-to-end.

**Realtime notifications: a `graphql-ws` client, connection-scoped to the current token.** `useNotifications` (`src/features/notifications/useNotifications.ts`) opens a subscription only while `accessToken` is set and tears it down when it changes — login/logout naturally reconnect/disconnect rather than needing an explicit "refresh the socket" call anywhere else. The token travels via `connectionParams` at `connection_init`, matching `wsInitFunc`'s expectation on the gateway (a WebSocket upgrade can't carry a normal `Authorization` header — see that function's own comment in `cmd/api-gateway/main.go`).

**Shared `Layout` + nav added, not part of the original auth-only scope**, because skills/jobs pages need somewhere to link to and from, and the notification bell needs a persistent place to live across routes. `/skills` and `/jobs*` are intentionally reachable without logging in first — this mirrors the schema itself (`createSkill`/`skills`/`jobs`/`createJob`/`job` are unauthenticated in Phase 1b, a known accepted gap, not new scope creep introduced here).

**Job creation's required-skills picker is a checkbox list, not a dropdown/multiselect widget** — `requiredSkillIds: [ID!]!` just needs zero or more IDs; a checkbox list needs no new dependency beyond the `Checkbox` shadcn primitive already added, and is more legible than a searchable combobox at the skill-taxonomy sizes this project will realistically have locally.

### Verification performed (the architecturally load-bearing part — full live proof, not just build/lint)

Brought up the complete local stack (throwaway Postgres, Kafka, RabbitMQ, Redis via `make up`, all five Go services with every env var set) rather than a subset, specifically because this phase's actual point is proving the asynchronous chain works, not just that the UI compiles:

- **`createSkill`/`skills`** — created two real skills through the exact mutation/query the frontend sends.
- **The full matching chain, live, not mocked**: registered a second ("candidate") user, called `addUserSkill` with a real skill ID, and confirmed jobs-service's Kafka consumer logged `"updated user skill snapshot"` for that exact user (proving the `user.skills.updated` → snapshot-projection path, the same one Phase 2 originally verified, still holds with the frontend driving it). Then called `createJob` requiring that skill and confirmed jobs-service logged `"processed job.posted event"` with `matched_users: 1`, and a follow-up `job(id)` query showed the candidate in `matches` with a real score and timestamp — this is exactly what `JobDetailPage` renders.
- **The realtime push, end to end, with a real WebSocket client**: wrote a small standalone Node script using the same `graphql-ws` package the frontend uses, authenticated it via `connectionParams` with the candidate's real token (not a stub), subscribed to `onNotification`, then triggered a second `createJob` for the same skill from a separate terminal. The subscriber received a live `job_match` push within seconds — proving the full Kafka → matching worker → RabbitMQ `notifications.realtime` → gateway consumer → Redis pub/sub → `graphql-ws` chain works with the exact client library and auth mechanism (`Authorization` in `connection_init`) the frontend's `useNotifications` hook uses.
- `npm run build` and `npm run lint` both clean throughout.
- Full teardown afterward: all five `go run` processes (and their actual child binary PIDs — killing only the `go run` wrapper PID leaves the compiled binary running and its port bound, a real gotcha hit and corrected mid-verification), the Node WS test script, and all Docker containers (Postgres, Kafka, RabbitMQ, Redis) were stopped/removed. Nothing was left running.
