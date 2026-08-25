# internal/platform

Every package here exists because *every* service in this monorepo needs
it, and needs the same behavior — building it once now, reused unchanged
by `users-service`/`skills-service`/`jobs-service` when they land in later
phases, is cheaper than re-solving the same problem four times or (worse)
solving it slightly differently each time and debugging the drift later.

## logger

A single zap logger config (structured JSON, always tagged with
`service` and — via `FromContext` — `request_id`). JSON, not zap's
human-readable console encoder, even for local dev: every log line will
eventually be scraped by something that parses records, not prose (Docker
logs, `kubectl logs`, and later maybe Loki/Grafana). Deciding that once
here means no service reinvents its own log shape.

## requestid

Generates (`x-request-id`) or extracts an incoming one, threads it through
`context.Context`, and provides gRPC client/server interceptors so it
survives a hop across the network. Why this matters: a single registration
call in the full system eventually traverses gateway → gRPC → RabbitMQ →
NestJS → Redis → WebSocket. Without a correlation ID riding along the
whole way, debugging *which* hop broke means grepping five services' logs
for a timestamp and hoping. This is called out in the plan as higher
priority than a log-aggregation stack (Grafana/Loki stays optional) — a
request ID with plain `docker logs`/`kubectl logs` beats a beautiful
dashboard with nothing to correlate.

## health

Implements the standard `grpc.health.v1.Health` service (works with
`grpcurl`/`grpc_health_probe`/future k8s gRPC probes) plus HTTP `/healthz`
and `/readyz`. These answer two different questions on purpose:
`/healthz` — "is the process alive" (no dependency checks; a slow Postgres
should never get a healthy process killed and restarted) — and `/readyz` —
"can this instance serve traffic right now" (DB reachable, etc.), which is
what a load balancer or k8s readiness probe should gate routing on.

## shutdown

One `signal.NotifyContext` → `grpcServer.GracefulStop()` → HTTP server
`Shutdown()` → ordered cleanup funcs sequence, reused by every
`cmd/<service>/main.go`. The payoff shows up the first time you Ctrl-C a
service with an open Kafka consumer group membership (a later phase) —
without a graceful stop, the next start stalls ~45s on rebalance. Small
amount of code, easy to skip, expensive to skip.

## db

The one place any service opens a Postgres connection, because the
settings here aren't arbitrary — they're forced by using Supabase's
Supavisor pooler in **transaction mode** (port 6543), which every service
shares since five+ services would otherwise blow through a free-tier
direct-connection limit. Transaction-mode pooling can hand a physical
connection to a different session between statements, which breaks
server-side prepared statements — hence `PreferSimpleProtocol: true` and a
small `MaxOpenConns` (~5) per service. See `docs/DECISIONS.md` for the
full trade-off. There's no live Supabase project at the time this was
written, so `db`'s tests only verify config-wiring logic (defaults, DSN
plumbing) — not an actual round trip.

## kafka

Phase 2's thin `github.com/twmb/franz-go`-backed producer/consumer pair —
see `docs/DECISIONS.md`'s "Phase 2 implementation notes" for why franz-go
over the alternatives. `Producer` degrades gracefully exactly like
`cache.Client`: an unset/unreachable `KAFKA_BROKERS` disables publishing
rather than failing the service to start, and every publish failure logs
at **Error** level (not Warn) and is swallowed — a dropped domain event is
real data loss, distinct from a Redis cache miss, but the triggering RPC's
primary write (e.g. the skill was actually saved to Postgres) already
succeeded, so the event stays best-effort per the documented "no
transactional outbox" gap. `Consumer` wraps a named consumer group and
integrates with `internal/platform/shutdown`: `Run(ctx, handler)` polls
until `ctx` is cancelled from a shutdown `CleanupFunc`, then `Close()`
sends the group a clean `LeaveGroup` — this is specifically what prevents
the zombie-consumer-group-member problem `shutdown`'s own doc comment
warns about, now that a Kafka consumer actually exists to leave one.

## jwks

Two sides of the same coin. On auth-service: generate (or load from env)
an RS256 keypair at startup, serve the public half as a JWKS document
(`Sign` issues tokens). On any verifier (api-gateway): fetch and cache
that JWKS by `kid` with a bounded refresh interval (`Client`/`GetKey`),
and `Verify` parses+validates an incoming token against it — the
verifier-side companion to `Sign`, added in Phase 1b when
`myProfile`/`updateProfile`/`addUserSkill` became the first operations
that actually needed request-time verification (see
`internal/gateway/authctx` and `docs/DECISIONS.md`; Phase 1a only proved
the fetch/cache plumbing worked, with nothing yet to verify against it).
The point of doing this instead of a shared HMAC secret in every
service's env vars: rotating a key becomes "change one config value on
auth-service, wait one cache TTL" instead of "coordinate a secret rollout
across every service that verifies a token."
