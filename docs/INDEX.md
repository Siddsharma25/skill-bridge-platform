# Documentation Index

Every document in this repo, organized by what you're trying to do. Start with the "Start here" row that matches your question, then branch out from there.

## Start here

| I want to... | Read |
|---|---|
| Understand what this app actually does | [`docs/PROJECT_OVERVIEW.md`](PROJECT_OVERVIEW.md) |
| See the whole architecture/tech stack/how services connect, at a glance | [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) |
| Understand why it's built this way (architecture, trade-offs) | [`docs/DECISIONS.md`](DECISIONS.md) |
| Get it running locally — frontend, one service, the whole stack, k8s, Jenkins | [`docs/RUNNING_LOCALLY.md`](RUNNING_LOCALLY.md) |
| Actually deploy it live | [`docs/DEPLOYMENT.md`](DEPLOYMENT.md) — your current to-do list |
| Know what's secure and what isn't | [`docs/SECURITY.md`](SECURITY.md) |

## Project-wide

- [`README.md`](../README.md) — repo layout, quick-start commands for frontend/backend/full Docker stack, and a "Deploying" summary (the detailed version lives in `docs/DEPLOYMENT.md`).
- [`docs/PROJECT_OVERVIEW.md`](PROJECT_OVERVIEW.md) — what the product does in plain terms: registration, skills, job postings, matching, notifications. What's real vs. simulated (no real email is sent, matching is simple overlap scoring, no roles yet). Register/login/profile now have a real frontend UI (see `frontend/README.md`); everything else is still exercised via the GraphQL API directly.
- [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) — the whole system at a glance: a diagram, a tech-stack table per component, how every piece talks to every other (gRPC/Kafka/RabbitMQ/Redis), how components work independently vs. as a system, what's not wired up yet, and an honest accounting of current unit test coverage.
- [`docs/RUNNING_LOCALLY.md`](RUNNING_LOCALLY.md) — every way to run this project, from fastest (frontend alone, one backend service) to heaviest (full Docker stack, local Kubernetes via `kind`, local Jenkins) — with a table pointing you at the right one for what you're doing.
- [`docs/DECISIONS.md`](DECISIONS.md) — the architecture bible. Every non-obvious choice across all 11 build phases (0 through 7), with the reasoning and trade-off behind each one, plus a running log of real bugs found and fixed along the way (Kafka's advertised-listener gotcha, k8s operator version drift, a NestJS lifecycle race, a JCasC schema mismatch, and more). Read the top-level sections for the "why" behind the system; read a specific "Phase N implementation notes" section for exactly how that phase was built and verified.
- [`docs/SECURITY.md`](SECURITY.md) — what's actually verified secure (bcrypt cost, RS256+JWKS, no SQL-injection surface, CORS) vs. genuinely still missing (no role/authorization system, no refresh-token rotation). Written against the code, not aspirational.
- [`docs/DEPLOYMENT.md`](DEPLOYMENT.md) — the step-by-step checklist for going from "builds and runs locally" to "live on the internet": Supabase, Render, Vercel, CORS, optional Google OAuth. This is the part that needs you specifically — nothing further can be automated.
- [`CLAUDE.md`](../CLAUDE.md) — project-wide conventions for anyone (human or AI) making changes here, including the efficiency conventions for delegating work to background agents.

## Backend (`backend/`)

- [`backend/README.md`](../backend/README.md) — how the Go services relate to each other, dev commands (`make gen`, `make test`, `make up`/`up-full`), and how to run the full Docker stack.
- [`backend/CLAUDE.md`](../backend/CLAUDE.md) — Go-specific conventions: the codegen workflow, error-handling pattern, and the exact steps to add a new service (points back to `CLAUDE.md`'s efficiency section rather than duplicating it).
- [`backend/proto/README.md`](../backend/proto/README.md) — why proto-first + `buf`, and how one `.proto` file becomes gRPC stubs.
- [`backend/internal/platform/README.md`](../backend/internal/platform/README.md) — the shared foundation every service builds on: structured logging, request-ID propagation, health/readiness, graceful shutdown, DB connection handling, JWKS. Read this before touching any service's `main.go`.
- Per-service READMEs — what each service does, why it's built the way it is, what to touch first if you're changing it:
  - [`backend/cmd/auth-service/README.md`](../backend/cmd/auth-service/README.md) — register/login, JWT issuance, JWKS, Google OAuth.
  - [`backend/cmd/users-service/README.md`](../backend/cmd/users-service/README.md) — profiles and self-reported skills.
  - [`backend/cmd/skills-service/README.md`](../backend/cmd/skills-service/README.md) — the shared skill taxonomy.
  - [`backend/cmd/jobs-service/README.md`](../backend/cmd/jobs-service/README.md) — job postings and the Kafka-driven matching worker.
  - [`backend/cmd/api-gateway/README.md`](../backend/cmd/api-gateway/README.md) — the GraphQL schema, auth verification, rate limiting, dataloaders, WebSocket subscriptions.
  - [`backend/cmd/allinone/README.md`](../backend/cmd/allinone/README.md) — the single-binary production shape combining all four services + the gateway.
  - [`backend/notification-service/README.md`](../backend/notification-service/README.md) — the NestJS service consuming RabbitMQ, including how its dead-letter-queue handling works.

## Infra practice (not the production deploy path — see `docs/DEPLOYMENT.md` for that)

- [`jenkins/README.md`](../jenkins/README.md) — the local, build-only Jenkins pipeline: what it proves, how to bring it up, why it gates nothing real.
- `k8s/` and `docker/` don't have their own top-level READMEs — their reasoning lives in `docs/DECISIONS.md`'s Phase 4 and Phase 5 sections respectively.

## Frontend (`frontend/`)

- [`frontend/README.md`](../frontend/README.md) — what's actually wired up (register/login/profile, so far), the GraphQL client/auth-store/protected-route pattern, the `login`-doesn't-return-`userId` gotcha and how the client works around it, and known gaps (no tests, no Google OAuth UI yet).
- [`frontend/CLAUDE.md`](../frontend/CLAUDE.md) — Node version requirement, path aliases, shadcn/ui conventions.
