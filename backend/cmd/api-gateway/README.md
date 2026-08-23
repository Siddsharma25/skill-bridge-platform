# api-gateway

The only thing the frontend ever talks to. Public-facing GraphQL over
HTTPS; everything behind it is private gRPC.

## What it does (Phase 1b)

- `Mutation.register`/`Mutation.login` — thin pass-throughs to
  auth-service, unauthenticated (Phase 1a).
- `Query.skills`/`Mutation.createSkill`, `Query.jobs`/`Query.job`/
  `Mutation.createJob` — thin pass-throughs to skills-service/
  jobs-service. Unauthenticated in Phase 1b — no role system exists yet
  (see `docs/DECISIONS.md`).
- `Query.myProfile`, `Mutation.updateProfile`, `Mutation.addUserSkill` —
  the **first authenticated operations** in this project. Each calls
  `requireUserID(ctx)` (`internal/gateway/graph/helpers.go`) first, which
  returns a GraphQL error unless `internal/gateway/authctx.Middleware`
  verified a bearer token for this request. See `docs/DECISIONS.md` for
  the full design.
- `Job.requiredSkills`, `Profile.skills`, `UserSkill.skill` — field
  resolvers that join across to skills-service by ID. Deliberately one
  call per parent object (a small N+1), marked
  `// TODO(phase 1c): dataloader` — see `docs/DECISIONS.md`.
- Health/readiness (`/healthz`, `/readyz`) and graceful shutdown, same
  pattern as every other service.
- GraphQL Playground at `/` in non-production environments.

## JWT verification middleware (`internal/gateway/authctx`)

`authctx.Middleware` wraps the `/query` handler: it extracts
`Authorization: Bearer <token>`, verifies it against auth-service's JWKS
(fetched/cached at startup, same client Phase 1a proved worked), and
stores the verified `sub` claim in the request context if valid. A
missing or invalid token is **not** rejected at this HTTP layer — the
same endpoint also serves unauthenticated operations — so rejection
happens per-resolver via `requireUserID`, as a normal GraphQL error rather
than a special-cased transport failure. The verified user ID is also
forwarded as gRPC metadata (`user_id`) on every call through
users-service's client connection specifically, via
`authctx.UnaryClientInterceptor`.

## Why GraphQL, and why the gateway is stateless

One flexible query language for the frontend beats stitching together
four services' worth of bespoke REST responses, and it gives later phases
a natural place to add DataLoader batching for the `requiredSkills`/
`skills`/`skill` N+1s described above. The gateway holds no database
connection and no business logic of its own — every resolver calls a
generated gRPC client. This is what lets it scale by replica count with
zero coordination: any instance can serve any request.

## Local run

Needs auth-service, skills-service, users-service, and jobs-service all
reachable (defaults: `localhost:9001`/`9002`/`9003`/`9004`):

```
cd backend
go run ./cmd/auth-service &
go run ./cmd/skills-service &
go run ./cmd/users-service &
go run ./cmd/jobs-service &
go run ./cmd/api-gateway
# GraphQL + Playground on :8080
```
