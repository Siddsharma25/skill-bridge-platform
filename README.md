# Skill Bridge Platform

A job/skill-gap matching platform — built as a hands-on learning project covering microservices, gRPC, Kafka, RabbitMQ, Redis, Kubernetes, GraphQL, WebSockets, and NestJS, at $0 infrastructure cost.

The full architecture, phased build order, and the trade-offs behind each decision live in [`docs/DECISIONS.md`](docs/DECISIONS.md) — read that before making structural changes.

## Repo layout

```
frontend/     React + Vite + TypeScript SPA
backend/      Go microservices (auth, users, skills, jobs, api-gateway) + notification-service (NestJS)
docker/       local dev infrastructure (Kafka, RabbitMQ, Redis) and full-stack compose
k8s/          Kubernetes manifests for local (kind) practice only — not the production deploy path
jenkins/      local Jenkins pipeline (build/test practice, not a real deploy gate)
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

## Status

This is being built incrementally, phase by phase — see `docs/DECISIONS.md` for what's live versus in progress. Production deployment is intentionally a trimmed-down subset of the full local/learning architecture (no Kafka/RabbitMQ in production); that gap is documented, not accidental.
