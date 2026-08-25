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

Production is a deliberately different, much simpler shape than the local/kind learning architecture above: one Render web service running `backend/cmd/allinone` (a single binary registering all four backend gRPC servers on loopback plus api-gateway's public HTTP/GraphQL/WebSocket server in one process — see `docs/DECISIONS.md`'s "Production deployment: a single allinone binary" section and its Phase 7 notes for the full reasoning) and one Vercel static deploy of `frontend/`. Kafka/RabbitMQ don't run in production at all — job matching, the welcome-email log, and realtime WebSocket push notifications are local/kind-only features; the synchronous GraphQL surface (register, login, createSkill, createJob, myProfile, addUserSkill) works identically either way.

Everything short of actually creating accounts is built and verified locally (see `docs/DECISIONS.md`'s Phase 7 notes for exactly what was proven): `backend/cmd/allinone`, `backend/Dockerfile.allinone`, `render.yaml`, `frontend/vercel.json`, and CORS on api-gateway (`internal/gateway/cors`, `CORS_ALLOWED_ORIGINS`). Getting a real deployment live needs a human to do the following — none of it can be done from this repo alone:

1. **Create a Supabase project** (free tier). Run `backend/migrations/000_bootstrap.sql` against it with an admin connection to create the four schemas/roles, then set a real password per role.
2. **Create a Render account**, connect this GitHub repo, and apply `render.yaml` as a Blueprint (it lives at the repo root and points at `backend/Dockerfile.allinone` via `rootDir: backend`). Fill in every env var the Blueprint marks as a secret via Render's dashboard: the four `*_DATABASE_URL` values from step 1 (Supavisor pooler connection strings, transaction mode, port 6543), a real `JWT_PRIVATE_KEY_PEM` (e.g. `openssl genrsa 2048`, reformatted to one line with literal `\n` — leaving this unset works for a quick test but invalidates every token on every restart), and optionally the `GOOGLE_OAUTH_*` trio and `REDIS_URL` (see `render.yaml`'s own comments for why Redis isn't provisioned by default and what's traded off by leaving it unset).
3. **Create a Vercel account**, import this repo, and set the project's Root Directory to `frontend` (a dashboard setting; `frontend/vercel.json` handles the build/output config and SPA routing once that's set). Set `VITE_API_URL` to `https://<your-render-service>.onrender.com/query` once step 2's URL exists.
4. **Circle back to Render** and set `CORS_ALLOWED_ORIGINS` to the real Vercel URL from step 3 (comma-separated if you also want `http://localhost:5173` to keep working for local dev against the live backend).
5. **Optional: create a Google Cloud OAuth client** (Web application type, a stable domain — not a Vercel preview URL) if Google login is wanted, and set `GOOGLE_OAUTH_REDIRECT_URL` to the deployed frontend's callback route.

## Status

This is being built incrementally, phase by phase — see `docs/DECISIONS.md` for what's live versus in progress. Production deployment is intentionally a trimmed-down subset of the full local/learning architecture (no Kafka/RabbitMQ in production); that gap is documented, not accidental.
