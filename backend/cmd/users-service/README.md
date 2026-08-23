# users-service

Owns profile data (`users.profiles`) and the skills a user claims to have
(`users.user_skills`) — distinct from auth-service's credential store
(`auth.credentials`) and from skills-service's skill taxonomy
(`skills.skills`).

## What it does

- `GetProfile(user_id)` — returns the profile, **lazily creating an empty
  one on first touch** if none exists yet (see "Lazy profile creation"
  below).
- `UpdateProfile(user_id, display_name?, bio?)` — same lazy-create
  behavior, then applies whichever fields were actually supplied (both
  optional, so a partial update never clobbers the other field with an
  empty string).
- `AddUserSkill(user_id, skill_id, proficiency)` — upserts one
  `(user_id, skill_id) -> proficiency` row.
- `ListUserSkills(user_id)` — returns every `(skill_id, proficiency)` pair
  for a user. Deliberately does **not** resolve skill names/categories —
  that's skills-service's data, joined in by the gateway (a small,
  intentional N+1 left for Phase 1c's dataloader work).
- Serves `grpc.health.v1.Health` plus HTTP `/healthz`/`/readyz`.

## Lazy profile creation (Phase 1b -> 2 migration note)

There's no Kafka `user.registered` consumer yet — that's Phase 2. Rather
than leave `GetProfile`/`UpdateProfile` erroring out for any user_id that
hasn't been explicitly provisioned, both RPCs create an empty profile row
(`display_name = ""`, `bio = ""`) the first time they're called for an
unrecognized `user_id`. The insert uses `ON CONFLICT (user_id) DO NOTHING`
followed by a re-fetch, so two concurrent first-touch requests for the
same user can't race into a duplicate-key error.

This is explicitly a **stand-in**, not the intended long-term design: once
Phase 2 adds Kafka, auth-service will publish `user.registered` and
users-service will consume it to provision the profile row at registration
time instead of waiting for a first read/write. The row shape
(`users.profiles`) doesn't change between the two approaches — only *when*
the row gets created does. See `docs/DECISIONS.md` for the same note in
one place across services.

## Why skill_id isn't validated against skills-service

`AddUserSkill` accepts any string as `skill_id` — users-service's
Postgres role cannot read `skills.*` (schema-per-service isolation), so it
has no way to check the ID is real without either a synchronous gRPC call
to skills-service (adds a hard runtime dependency to a write path that
doesn't otherwise need one) or an event-carried-state-transfer projection
(not justified yet for a single foreign ID). Accepted gap for Phase 1b: an
invalid ID is only ever noticed when the gateway tries to resolve it for
display.

## Degraded-start behavior

Same pattern as every service in this codebase: starts and serves health
checks even without `DATABASE_URL` set/reachable; every profile/skill RPC
returns `Unavailable` until a real database is configured.

## Local run

```
cd backend
go run ./cmd/users-service
# gRPC on :9003, HTTP (health) on :8083
grpcurl -plaintext localhost:9003 list
curl localhost:8083/healthz
```
