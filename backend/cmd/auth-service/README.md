# auth-service

Owns password credentials and access-token issuance for the whole
platform. It's the only service that ever sees a plaintext password or a
password hash — every other service (including the gateway) trusts a
verified JWT instead of asking auth-service to re-check anything.

## What it does

- `Register(email, password)` — hashes the password with bcrypt, inserts
  a row into `auth.credentials`, and returns an access token immediately
  (so a client can skip a second `Login` call right after signup). Also
  publishes two best-effort RabbitMQ events after the account is durably
  created (neither can fail `Register`): `notifications.email` (Phase 3,
  consumed by notification-service) and `notifications.realtime` (Phase
  3.5, consumed by api-gateway's realtime bridge and relayed to a live
  `onNotification` GraphQL subscription — see
  `cmd/api-gateway/README.md`). The realtime one is architecturally
  complete but **not** this project's live-verification trigger for the
  subscription — see `docs/DECISIONS.md`'s Phase 3.5 notes for why
  (registration happens before a client could possibly have a token to
  subscribe with).
- `Login(email, password)` — verifies the bcrypt hash and returns a fresh
  access token.
- Serves its own JWKS document at `/.well-known/jwks.json` so any verifier
  can fetch and cache its public signing key.
- Serves `grpc.health.v1.Health` plus HTTP `/healthz`/`/readyz`.

## Why JWKS + RS256, not a shared HMAC secret

A shared symmetric secret (HS256) means every service that verifies a
token needs the *same* secret that signs one — rotating it means touching
every service's config at once, and any service that can verify a token
can also forge one. RS256 with a published JWKS means only auth-service
ever holds the private key; everyone else fetches the public half over
HTTP and caches it by `kid`. Rotating the key is a config change on one
service, not a coordinated secret rollout.

The keypair is generated fresh at startup if `JWT_PRIVATE_KEY_PEM` isn't
set in the environment — convenient for local dev (no key to generate and
paste by hand), but every restart invalidates every outstanding token and
a multi-instance deployment would have each instance mint incompatible
keys. Set `JWT_PRIVATE_KEY_PEM` explicitly anywhere that isn't a single
local dev process.

## Why bcrypt, not a faster hash

Password hashing is one of the few places where being *slow on purpose* is
the point — bcrypt's cost factor makes offline brute-forcing a stolen hash
expensive without meaningfully slowing down a legitimate login (which only
ever hashes once). This service uses bcrypt's library default cost (10):
high enough to be considered adequate as of 2026, low enough that local
dev/test runs and CI stay fast. See `docs/DECISIONS.md` if that ever needs
revisiting.

## Why this is a separate service from users-service

Splitting "who can log in" (credentials) from "who someone is" (profile
data, in `users-service`, a later phase) means the credential store never
needs to know about profile fields, and a profile schema change never
touches anything security-sensitive. It also means auth-service's schema
(`auth.credentials`) can have much tighter access controls than a general
profile table.

## Degraded-start behavior

There is no live Supabase project yet at this phase of the build. Rather
than fail to start without `DATABASE_URL`, this service logs a clear
warning and starts anyway, serving health checks and JWKS normally.
`Register`/`Login` return a `Unavailable` gRPC status until a real
database is configured — this keeps local dev, CI, and the gateway's
startup path unblocked by an external dependency that doesn't exist yet.

## Local run

```
cd backend
go run ./cmd/auth-service
# gRPC on :9001, HTTP (health + JWKS) on :8081
grpcurl -plaintext localhost:9001 list
curl localhost:8081/healthz
curl localhost:8081/.well-known/jwks.json
```
