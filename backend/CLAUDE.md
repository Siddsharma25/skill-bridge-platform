# backend/CLAUDE.md

Go-specific conventions for this half of the repo. See root `CLAUDE.md`
for the whole-project map and `docs/DECISIONS.md` for the architecture
reasoning. This file is what Claude Code (or a human) should read before
touching anything under `backend/`.

## Regenerating code

- **`make gen`** (from `backend/`) regenerates two things: gRPC stubs
  (`buf generate`, from `proto/*.proto` into `gen/`) and the gqlgen
  GraphQL layer (`graph/generated/`, `graph/model/`, resolver stubs, from
  `internal/gateway/graph/schema.graphqls`). Run it after editing either.
- **`gen/` and `internal/gateway/graph/generated/` +
  `internal/gateway/graph/model/` are committed to git, not gitignored.**
  A clean checkout and CI build without a hidden codegen step. Never
  hand-edit files under these paths — regenerate instead. `.golangci.yml`
  excludes them from linting for the same reason.
- `internal/gateway/graph/schema.resolvers.go` and `resolver.go` **are**
  hand-edited — gqlgen only regenerates the method stubs it recognizes and
  preserves everything else, per the "DO NOT EDIT" header only applying to
  the generated packages, not the resolver file itself.
- `buf generate` shells out to `protoc-gen-go`/`protoc-gen-go-grpc` as
  local binaries (not buf.build remote plugins) so `make gen` works
  offline and CI doesn't depend on the BSR being reachable. `make setup`
  installs them (`go install .../protoc-gen-go@latest`, etc.) into
  `$(go env GOPATH)/bin` — make sure that's on `PATH`.

## Error handling pattern

- gRPC handlers return `status.Error(codes.X, "message")` — never a bare
  `error` or a Go stdlib error type — so the gateway (and any future
  direct gRPC client) gets a machine-readable code, not just a string to
  pattern-match on. See `internal/auth/server.go` for the pattern: map
  domain outcomes to codes explicitly (`InvalidArgument` for bad input,
  `AlreadyExists` for a duplicate, `Unauthenticated` for bad credentials —
  and deliberately the *same* message for "no such user" and "wrong
  password", since distinguishing them is a user-enumeration leak).
- The gateway's resolvers unwrap a gRPC status error's `.Message()` into a
  plain GraphQL error rather than let gqlgen's default `rpc error: code =
  ... desc = ...` wrapping leak transport details to a GraphQL client (see
  `translateGRPCError` in `internal/gateway/graph/schema.resolvers.go`).
- Every exported function that can fail returns `error` as its last return
  value (standard Go); internal helpers that truly cannot fail don't.
- Log the error where it's handled (with `zap.Error(err)`), not at every
  layer it passes through — a handler that logs and then also returns an
  error the caller logs again just duplicates the same failure in two log
  lines.

## Adding a new service

Follow the pattern `auth-service` established:

1. `proto/<service>/v1/<service>.proto` — define the gRPC contract first.
   `make gen` to produce stubs.
2. `internal/<service>/` — business logic: a GORM model (`TableName()`
   returning `"<schema>.<table>"`, matching the service's own Postgres
   schema — never another service's), a `Server` struct implementing the
   generated `*ServiceServer` interface, constructed via `NewServer(db,
   ...)` where `db` may be `nil` (see the degraded-start pattern below).
3. `migrations/<service>/NNNNN_description.sql` — goose migration(s). Add
   the service's schema + role to `migrations/000_bootstrap.sql` if not
   already there (it currently pre-creates `auth`, `users`, `skills`,
   `jobs`, `notifications` schemas/roles so this step is usually already
   done).
4. `cmd/<service>/main.go` — copy `cmd/auth-service/main.go`'s shape:
   `godotenv.Load()` gated on `ENV != "production"`, connect DB via
   `internal/platform/db` without failing hard on error (log a warning,
   keep `*gorm.DB` nil, let handlers return `Unavailable`), wire
   `internal/platform/{logger,requestid,health,shutdown}`, register the
   gRPC service + health + reflection, serve gRPC and HTTP on separate
   ports, `shutdown.Wait(...)` at the end.
5. `Dockerfile.<service>` — copy `Dockerfile.auth-service`, swap the
   `cmd/` path and `EXPOSE` ports.
6. A short `cmd/<service>/README.md` — what it does, why it's built this
   way, what to touch first. Write it alongside the code, not after.
7. Add the service to `.golangci.yml`'s exclusions only if it has its own
   generated subpackage (most won't).

## Efficiency conventions

See the root `CLAUDE.md`'s "Efficiency conventions for background agents"
section — it's project-wide (backend, frontend, docker/k8s, CI alike), not
duplicated here to avoid the two copies drifting out of sync.

## Don't

- Don't add `google.api.http` annotations / grpc-gateway to any service
  except skills-service (a later phase) — see `docs/DECISIONS.md` for why
  REST is scoped to one service on purpose.
- Don't have one service's GORM model read another service's schema. If a
  matching-worker-style cross-service read seems necessary, the answer is
  an event-carried-state-transfer projection (see jobs-service's
  `user_skill_snapshot` in a later phase), not a shared connection string.
- Don't bump `db.Config.MaxOpenConns` casually — it's sized for Supavisor's
  shared transaction-mode pooler budget across every running service, not
  per-service throughput.
- Don't skip `internal/platform/shutdown` in a new `cmd/<service>/main.go`
  "because it's just a quick prototype" — it's a few lines to wire in and
  the alternative is a zombie process on every Ctrl-C once anything holds
  a long-lived connection (a Kafka consumer, in a later phase).
