# api-gateway

The only thing the frontend ever talks to. Public-facing GraphQL over
HTTPS; everything behind it is private gRPC.

## What it does (Phase 1a)

- `Mutation.register(email, password)` / `Mutation.login(email, password)`
  — both thin pass-throughs to auth-service's gRPC `Register`/`Login`.
- Fetches auth-service's JWKS once at startup (proves the JWKS
  fetch/cache plumbing works end-to-end) — not yet wired into
  request-time verification middleware, because Phase 1a has no
  authenticated query to protect yet. That lands with the first query
  that actually needs a verified subject.
- Health/readiness (`/healthz`, `/readyz`) and graceful shutdown, same
  pattern as every other service.
- GraphQL Playground at `/` in non-production environments.

## Why GraphQL, and why the gateway is stateless

One flexible query language for the frontend beats stitching together four
services' worth of bespoke REST responses, and it gives later phases (a
`jobs { requiredSkills }`-shaped N+1) a natural place to add DataLoader
batching. The gateway holds no database connection and no business logic
of its own — every resolver calls a generated gRPC client. This is what
lets it scale by replica count with zero coordination: any instance can
serve any request.

## Local run

Needs auth-service reachable (default `localhost:9001`) to fetch JWKS at
startup and to serve `register`/`login`:

```
cd backend
go run ./cmd/auth-service &   # or in a separate terminal
go run ./cmd/api-gateway
# GraphQL + Playground on :8080
```
