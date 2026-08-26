# Running Everything Locally

Every way to bring this project up, in order from "fastest inner loop" to "closest to the full learning architecture." Pick the one that matches what you're actually doing — you don't need the heaviest option just to check something works.

## Which one do I actually want?

| I want to... | Use |
|---|---|
| Work on the frontend UI | [Frontend only](#frontend-only) |
| Change one backend service and iterate fast | [One backend service](#one-backend-service) |
| Exercise the whole API (register → login → jobs → matching) without Docker | [All backend services locally](#all-backend-services-locally-no-docker) |
| Prove the whole thing works from a clean checkout, containerized | [Full stack via Docker](#full-stack-via-docker) |
| Practice Kubernetes | [Local Kubernetes (kind)](#local-kubernetes-kind) |
| Practice Jenkins | [Local Jenkins](#local-jenkins) |
| See distributed tracing/logs/metrics in one dashboard | [Observability stack](#observability-stack) |

## Prerequisites

- **Node 22** (`frontend/.nvmrc` / `backend/notification-service/.nvmrc` pin this — `nvm use` in each directory)
- **Go 1.27+**, `buf`, `golangci-lint`, `goose`, `grpcurl` (backend/`make setup` checks all of these are present)
- **Docker Desktop** running, for anything past "one backend service"
- No live Supabase project needed for any of this — every option below uses a throwaway local Postgres instead (see each section)

## Frontend only

```bash
cd frontend
npm install
npm run dev
```

Note: the frontend now covers the full feature set (auth, skills, jobs, matching, realtime notifications — see `frontend/README.md`). By default it points `VITE_API_URL` at `http://localhost:8080/query`, so pair this with the backend running to see it do anything: `auth-service` + `users-service` + `api-gateway` alone gets you register/login/profile; add `skills-service` + `jobs-service` + Kafka/RabbitMQ/Redis (see the next section) for skill/job creation, matching, and the live notification bell to actually work end-to-end.

## One backend service

Fastest way to iterate on a single service's code:

```bash
cd backend
cp .env.example .env   # then edit — see below
go run ./cmd/auth-service   # or skills-service, users-service, jobs-service, api-gateway
```

Every service **degrades gracefully** with no `DATABASE_URL`/`REDIS_URL`/`KAFKA_BROKERS`/`RABBITMQ_URL` set — it'll start and serve `/healthz`, it just can't do anything that needs the missing piece. See `docs/ARCHITECTURE.md`'s "How components work independently" section for why this is a deliberate design property, not a fallback to work around.

## All backend services locally (no Docker)

To actually exercise the full API (register → login → createSkill → createJob → myProfile), you need: a throwaway Postgres, the infra services (Redis/Kafka/RabbitMQ), and all 5 Go services + notification-service running together.

```bash
# 1. Throwaway Postgres (not part of any committed compose file — see docs/DECISIONS.md)
docker run --rm -d --name sbp-local-postgres -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:16-alpine
psql "postgres://postgres:postgres@localhost:5432/postgres" -f backend/migrations/000_bootstrap.sql
# then ALTER ROLE each service role's password to something known (auth_service, skills_service, users_service, jobs_service, notification_service)

cd backend

# 2. Apply each service's own migrations (see Makefile's migrate-up comment for why -table matters)
DATABASE_URL="postgres://postgres:postgres@localhost:5432/postgres" make migrate-up

# 3. Infra: Redis + Kafka + RabbitMQ
make up

# 4. Each service, in its own terminal, with backend/.env configured per-service (see docs above)
go run ./cmd/auth-service
go run ./cmd/skills-service
go run ./cmd/users-service
go run ./cmd/jobs-service
go run ./cmd/api-gateway

# 5. notification-service, in its own terminal
cd notification-service
cp .env.example .env   # edit DATABASE_URL/RABBITMQ_URL
npm install
npm run start
```

Then hit `http://localhost:8080/query` (GraphQL, Playground available in dev) or open a WebSocket for `onNotification`. `make down` / `make nuke` tears the infra back down when you're done (`nuke` also drops volumes).

## Full stack via Docker

Proves the whole thing works from a clean checkout, fully containerized — this is what Phase 4 built and verified.

```bash
cp docker/.env.example docker/.env   # fill in real *_DATABASE_URL values — see file's own comment
                                       # for the throwaway-Postgres recipe if you don't have real Supabase yet
cd backend
make up-full
```

`make down-full` / `make nuke-full` tear it down. See `backend/README.md` and `docs/DECISIONS.md`'s Phase 4 notes for exactly what's wired up.

## Local Kubernetes (kind)

This is explicitly a **learning sandbox**, not the production deploy path, and it's genuinely involved — a throwaway Postgres, real `Secret` objects, and two operators (Strimzi for Kafka, the RabbitMQ Cluster Operator) all need to come up in the right order. Full command-by-command detail is in `docs/DECISIONS.md`'s Phase 5 implementation notes; the shape is:

```bash
cd backend
make kind-up      # creates the kind cluster, installs + waits for both operators
make kind-load    # builds all 6 service images and loads them into the cluster
# then: apply k8s/strimzi/kafka-cluster.yaml and k8s/rabbitmq/rabbitmq-cluster.yaml,
# apply a throwaway Postgres, run bootstrap+migrations against it, create each
# service's real Secret (see k8s/base/*/secret.example.yaml for the shape),
# then: kubectl apply -k k8s/overlays/kind
```

`kind delete cluster --name skill-bridge` tears it down — do this before starting the full Docker stack or Jenkins, since this machine's disk/RAM budget can't hold more than one resource-heavy option at a time (see `docs/DECISIONS.md`'s Environment Reality Check).

## Local Jenkins

Also a **practice sandbox** — gates nothing real, GitHub Actions is the only pipeline an actual merge depends on. See `jenkins/README.md` for the full walkthrough; the shape is:

```bash
docker compose -f jenkins/docker-compose.jenkins.yml up -d --build
# Jenkins provisions itself via Configuration as Code — no manual setup wizard.
# Open http://localhost:8090 (admin/admin) or trigger a build via the REST API
# (exact curl commands in jenkins/README.md).
docker compose -f jenkins/docker-compose.jenkins.yml down -v   # tear down when done
```

## Observability stack

Jaeger (distributed tracing), Loki+Promtail (centralized logging), Prometheus (metrics), and Grafana (one dashboard over all three) — see `backend/README.md`'s own section and `docs/DECISIONS.md`'s tracing/logging notes for the full design and how it was verified.

```bash
docker compose -f docker/docker-compose.observability.yml up -d
# Then run api-gateway (and any other instrumented service, e.g. skills-service)
# with OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317 to start sending traces.
```

Jaeger UI: http://localhost:16686 · Grafana: http://localhost:3300 (anonymous admin, Prometheus/Loki/Jaeger pre-provisioned as datasources) · Prometheus: http://localhost:9090.
