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

## Realtime notifications (Phase 3.5)

`Subscription.onNotification: Notification!` streams every realtime
notification published for the authenticated caller (auth-service on
`Register`, jobs-service's matching worker on `job.matched`), over a
`graphql-ws` WebSocket connection at the same `/query` route the regular
GraphQL handler serves — `gqlgen`'s `handler.New` (not the deprecated
`NewDefaultServer`, which already registers its own unauthenticated
`transport.Websocket{}`) is wired up explicitly in `main.go` specifically
so `transport.Websocket.InitFunc` (`wsInitFunc`) can be configured.

- **Auth is different for a subscription than a query/mutation.** A
  WebSocket upgrade request doesn't carry a bearer header on every
  message the way HTTP does, so the `graphql-ws` protocol has the client
  send a token once, in the `connection_init` message's payload.
  `wsInitFunc` extracts it (`Authorization: Bearer <token>` or a bare
  `token` field), verifies it via `authctx.VerifyToken` (the same
  verifier `authctx.Middleware` uses for HTTP), and — unlike the HTTP
  middleware, which never rejects a request itself — rejects the
  connection outright on failure (closed with code 4401, the
  `graphql-ws` protocol's own "Unauthorized" code) before any
  `subscribe` message on that connection is ever accepted. The verified
  identity becomes that one connection's base context; a fresh
  `wsConnection` (and a fresh `InitFunc` call) exists per accepted
  socket, so it can never leak across connections.
- **The bridge:** api-gateway's own `internal/gateway/realtime.Bridge`
  consumes RabbitMQ's `notifications.realtime` queue (a new
  `rabbitmq.Consumer`, wired in `main.go` alongside the HTTP server) and
  republishes each message onto Redis `PUBLISH realtime:user:<user_id>`.
  The `onNotification` resolver (`schema.resolvers.go`) opens a Redis
  `SUBSCRIBE` on the caller's own channel and forwards each message into
  the channel gqlgen streams back to the client — cleaned up (Redis
  unsubscribe, closing the output channel) the moment the connection's
  context is cancelled, so a dropped WebSocket client can't leak a live
  Redis subscription. This is deliberately mediated through Redis
  pub/sub rather than an in-memory map, so it works correctly across
  multiple gateway replicas with no redesign — see `docs/DECISIONS.md`.
- **Live verification uses `job.matched`, not `Register`'s welcome
  notification**, even though both publish to `notifications.realtime`:
  a registration's publish happens before the new user could possibly
  have a token to open a subscription with, and Redis pub/sub has no
  replay buffer, so that race is lost by design. See
  `docs/DECISIONS.md`'s Phase 3.5 notes and
  `internal/gateway/livetest/subscription_live_test.go` (build-tag
  `live`) for the actual proof: a real `graphql-transport-ws` client,
  hand-speaking the protocol, receiving a live `job_match` push.

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
