# proto/

This directory is the source of truth for every gRPC contract in the
platform. Nothing in `internal/` or `cmd/` should be considered the "real"
API shape — the `.proto` files are, and `gen/` is a derived artifact of
them.

## Why proto-first

Writing the contract before the implementation (rather than generating a
proto from Go structs, or skipping protos and hand-rolling REST handlers)
buys two things this project actually needs:

1. **A contract that isn't tied to one language.** `notification-service`
   is NestJS/TypeScript; everything else is Go. A shared `.proto` is the
   one artifact both sides can generate stubs from without one language's
   idioms leaking into the other's.
2. **Automatic breaking-change detection.** `buf breaking` in CI compares
   every PR's protos against `main`'s. A field renumbered, a required field
   added, an RPC removed — these are caught by a lint rule before merge,
   not discovered by a confused client three services away at runtime.
   This is the actual payoff of being proto-first; skipping it and still
   hand-writing protos would mean paying the tooling cost without the
   benefit.

## Why buf, not raw protoc

`buf` replaces `protoc` + a hand-maintained `protoc` invocation per
language/service with one declarative config (`buf.yaml`, `buf.gen.yaml`).
It also ships `buf lint` (style/consistency rules like "always use
`v1` package suffixes") and `buf breaking` (see above) as first-class
commands, not a linting plugin nobody remembers to run.

## Layout

```
proto/<service>/v1/*.proto   # one package per service per major version
```

`v1` in the path is deliberate from day one: a `v2` (for a breaking
redesign, say) lives alongside `v1` rather than replacing it in place, so
existing consumers of `v1` don't break the moment a new version ships.

## Adding or changing a contract

1. Edit or add a `.proto` file under `proto/<service>/v1/`.
2. Run `make gen` (from `backend/`) to regenerate `gen/`.
3. Review the diff in `gen/` like any other generated-but-committed code.
4. `buf lint` and `buf breaking` run in CI automatically — fix anything
   they flag before merging, rather than suppressing the check.

## REST/OpenAPI

Only **skills-service** (a later phase) gets a generated REST surface via
`protoc-gen-grpc-gateway` + `protoc-gen-openapiv2`, as a deliberate, scoped
learning exercise — not all four services. Debug everything else with
`grpcurl` (reflection is enabled on every service) or `buf curl`. See
`docs/DECISIONS.md` for the full reasoning.
