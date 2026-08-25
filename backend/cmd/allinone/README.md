# cmd/allinone

**What it does:** one process registering all four backend services'
gRPC servers (auth, skills, users, jobs) on loopback-only addresses
(`127.0.0.1:9001`-`9004`), plus api-gateway's HTTP/GraphQL/WebSocket
server as the single public-facing listener (`0.0.0.0:$PORT`). This is
Phase 7's production deploy target — the one image `render.yaml` points
Render at.

**Why it's built this way:** Render's free tier is realistically one web
service (background workers are paid-only, free services spin down after
~15 min idle). Running the full five-service mesh live for $0 isn't
realistic, and a gateway fanning out to four separately-sleeping services
would serially cold-start all of them on one request. Collapsing
everything into one process gives one cold start, with the four backend
gRPC ports never reachable from outside the process at all (loopback
only) — see `docs/DECISIONS.md`'s "Production deployment: a single
allinone binary" section and its Phase 7 implementation notes for the
full reasoning, including exactly what this trims relative to the
local/kind deployment (no Kafka/RabbitMQ, hence no job matching, no
welcome-email log, no realtime WebSocket push in production).

**How it fits the rest of the system:** this file is assembly, not new
business logic. Every `NewServer` call, every gRPC/HTTP server
construction, and the gateway's resolver/gRPC-client wiring is copied
unchanged from `cmd/auth-service`, `cmd/skills-service`,
`cmd/users-service`, `cmd/jobs-service`, and `cmd/api-gateway`
respectively — see each of those for the "why" behind its own piece. What
this file actually adds:

- Fixed loopback addresses for the four backend services (no service
  discovery needed — it's all one process).
- `dbGetenv`/`connectDB`: each service still gets its own scoped
  Postgres role's connection string (`AUTH_DATABASE_URL`,
  `SKILLS_DATABASE_URL`, `USERS_DATABASE_URL`, `JOBS_DATABASE_URL` — same
  naming `docker/docker-compose.yml` established in Phase 4) despite
  sharing one process-wide environment.
- One `shutdown.Wait` call covering all four gRPC servers (via
  `shutdown.Options.GRPCServers`, new in Phase 7 —
  see `internal/platform/shutdown`) plus every HTTP server, in one
  graceful sequence.

**What to look at first to change it:** if you're changing one service's
business logic, change it in `internal/<service>/` or
`cmd/<service>/main.go` as usual — this file will pick it up
automatically since it calls the same `NewServer` constructors. Only
touch this file directly for something specific to the single-process
deployment shape itself (a new fixed port, a new shared client
connection, a change to the shutdown sequence).

**What's NOT here:** notification-service (NestJS). Per the plan,
Kafka/RabbitMQ async flows don't exist in production — see the package
doc comment at the top of `main.go` for the full accounting of what that
means for `job_matches`, the welcome-email log, and `onNotification`.

**Env vars:** see `render.yaml` (the source of truth for what Render sets)
and `backend/.env.example`'s existing per-service sections — this binary
reads the same variable names every other `cmd/<service>/main.go` does,
except `DATABASE_URL` (see `dbGetenv` above) and `PORT`/`HTTP_PORT` (fixed
constants here; `PORT` is reserved for the gateway's public port, Render's
own convention).
