# Skill Bridge Platform

A job/skill-gap matching platform — built as a hands-on learning project covering microservices, gRPC, Kafka, RabbitMQ, Redis, Kubernetes, GraphQL, WebSockets, and NestJS, at $0 infrastructure cost.

**New here? Start at [`docs/INDEX.md`](docs/INDEX.md)** — it maps every document in this repo to the question it answers. The full architecture, phased build order, and the trade-offs behind each decision live in [`docs/DECISIONS.md`](docs/DECISIONS.md) — read that before making structural changes.

## Repo layout

```
frontend/     React + Vite + TypeScript SPA
backend/      Go microservices (auth, users, skills, jobs, api-gateway) + notification-service (NestJS)
docker/       local dev infrastructure (Kafka, RabbitMQ, Redis) and full-stack compose
k8s/          Kubernetes manifests for local (kind) practice only — not the production deploy path
jenkins/      local Jenkins pipeline (build/test practice, not a real deploy gate)
infra/aws/    optional CloudFormation stack that keeps the live Render/Supabase deploy warm
docs/         architecture decisions and per-area learning notes
```

## Quick start

**Frontend**
```
cd frontend
npm install
npm run dev
```

**Backend** (see `backend/CLAUDE.md` for the full command list once services exist)
```
cd backend
make setup
make gen
go run ./cmd/auth-service
```

**Full stack via Docker** (Phase 4 — all 6 backend services + Redis/Kafka/RabbitMQ, one clean-checkout `up`)
```
cp docker/.env.example docker/.env   # fill in real DATABASE_URL values first
cd backend && make up-full
```
See `backend/README.md`'s "Running the full stack via Docker" section and
`docs/DECISIONS.md`'s Phase 4 notes for what's actually wired up, how
`DATABASE_URL` is supplied, and what's still deliberately out of scope
(no local Postgres container — single shared Supabase instance by design).

## Deploying

Production is a deliberately different, much simpler shape than the local/kind learning architecture above: one Render web service running `backend/cmd/allinone` (a single binary registering all four backend gRPC servers on loopback plus api-gateway's public HTTP/GraphQL/WebSocket server in one process) and one Vercel static deploy of `frontend/`. Kafka/RabbitMQ don't run in production at all — job matching, the welcome-email log, and realtime WebSocket push notifications are local/kind-only features; the synchronous GraphQL surface (register, login, createSkill, createJob, myProfile, addUserSkill) works identically either way.

Everything short of actually creating accounts is built and verified locally. **See [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) for the full step-by-step checklist** (Supabase, Render, Vercel, CORS, optional Google OAuth, optional Sentry error tracking, optional AWS keep-alive Lambda) and `docs/DECISIONS.md`'s Phase 7 notes for the reasoning and verification log behind it.

## Status

This is being built incrementally, phase by phase — see `docs/DECISIONS.md` for what's live versus in progress. Production deployment is intentionally a trimmed-down subset of the full local/learning architecture (no Kafka/RabbitMQ in production); that gap is documented, not accidental.
