# Security Posture

What's actually in place, verified against the code (not aspirational) — and what's genuinely still missing. Cross-references `docs/DECISIONS.md` where a security-relevant choice is really an architecture choice with a security angle, rather than duplicating that reasoning here.

## Authentication

- **Passwords**: bcrypt, `bcrypt.DefaultCost` (10) — the widely-used floor, chosen over a higher cost to keep legitimate logins and local dev/test runs fast (see `internal/auth/server.go`). Minimum 8 characters enforced server-side; no forced complexity rules (uppercase/symbol/etc.) — this follows current NIST guidance (length matters more than composition rules, which mostly just push users toward predictable substitutions), not an oversight.
- **No user enumeration via login**: a nonexistent email and a wrong password return the *identical* `Unauthenticated` error and message. Don't special-case one of them later without re-checking this.
- **JWTs are RS256 (asymmetric), not a shared HMAC secret.** auth-service holds the private key and publishes the public half at `/.well-known/jwks.json`, keyed by `kid`; api-gateway fetches and caches it. This means rotating the signing key is a one-service config change, not a coordinated secret rollout across every service that verifies tokens.
- **Dev-mode key generation**: if `JWT_PRIVATE_KEY_PEM` isn't set, auth-service generates a throwaway keypair at startup — deliberate for local dev, but it means every restart invalidates all outstanding tokens. The code has an explicit warning comment; **production deployments must set `JWT_PRIVATE_KEY_PEM` explicitly.**
- **Google OAuth**: account-linking by email (a Google login for an email that already has a password-based `auth.credentials` row links to it rather than creating a duplicate account) — see `docs/DECISIONS.md` and `internal/auth/oauth.go`'s test suite for the exact rules. Degrades to a clear `FailedPrecondition` error when Google credentials aren't configured, rather than a confusing failure.
- **No refresh-token rotation yet** — access tokens are short-lived (1 hour) with no refresh flow. A logged-in session just expires; there's no rotation-on-refresh, no reuse detection. Acceptable for the current build stage; needs solving before this is a real product (see `docs/DECISIONS.md`'s Phase 1a notes on the deferred cross-origin auth story).

## Authorization

- **api-gateway is the only JWT verifier.** It forwards a verified `user_id`/`user_role` to backend services as gRPC metadata; backend services trust this rather than re-verifying independently. This is safe on a private network (local dev, docker-compose, k8s ClusterIP) where nothing but the gateway can reach those services. **Resolved for the actual production deployment, not deferred**: the plan's Phase 7 concern assumed each backend service might get its own public Render URL, which would have needed a shared-secret header (`x-internal-api-key`) as defense-in-depth. `cmd/allinone`'s actual shape (Phase 7) makes that moot instead — the four backend gRPC servers bind `127.0.0.1` only, inside the same process/container as the gateway, so there is no public network path to them at all for a shared secret to defend. See `docs/DECISIONS.md`'s Phase 7 implementation notes.
- **Basic RBAC now exists** — a `role` claim (`user`/`admin`) on every JWT, checked by `createSkill` via `requireAdmin` (see `docs/DECISIONS.md`'s RBAC notes). Deliberately narrow: one gated operation, no self-service promotion path (an admin is bootstrapped via a direct SQL update, not a feature), and `createJob` is still open to any caller — this isn't a general permissions/ACL system, just a real, working example of the mechanism. Still a real gap if this were ever a product: no per-resource ownership (a job has no "poster" who can edit/delete only their own), no audit trail on role changes, no invite flow.
- Mutations that *are* protected (`myProfile`, `updateProfile`, `addUserSkill`) always source the acting user's ID from the verified JWT context, never from a client-supplied argument — there's no way to pass another user's ID and edit their profile.

## Input handling & injection

- **No SQL injection surface found** — every database call goes through GORM's parameterized query builder; grepped the whole backend for string-concatenated/`Sprintf`-built SQL and found none. Keep it that way: never build a query with `fmt.Sprintf` and a user-supplied value.
- Input validation is done inline per-handler (email format/non-empty, password length, etc.) rather than via a shared validation library — functionally fine, just worth knowing it's not centralized if a new field needs the same treatment elsewhere.

## Network-facing surfaces

- **CORS is now configured on api-gateway** (Phase 7, `internal/gateway/cors`): an explicit allowlist via `CORS_ALLOWED_ORIGINS` (comma-separated), defaulting to `http://localhost:5173` for local dev, never `*`. A matching `Origin` gets that exact origin echoed back in `Access-Control-Allow-Origin` (plus `Vary: Origin`); a non-matching or missing origin gets no CORS headers and passes through unaffected (the browser's own same-origin enforcement is the actual block point — there's no server-side rejection to get wrong). Verified live: a request with an allowed `Origin` gets the header back with the matching value; one with a disallowed `Origin` doesn't. **Partially closed, not fully**: the allowlist is only as good as what's actually configured — `CORS_ALLOWED_ORIGINS` still needs the real Vercel URL added once the frontend is deployed (see `render.yaml` and the root README's "Deploying" section); until then, only local dev's origin is trusted.
- **Rate limiting** on api-gateway (Redis-backed, fixed-window, keyed by user ID when authenticated, else IP) — see `docs/DECISIONS.md`'s Phase 1c notes for the exact threshold/window. **Fails open** if Redis is unreachable (logs and allows the request through) — a deliberate availability-over-strictness choice for a $0, single-instance-Redis project; know this before assuming rate limiting is a hard guarantee.
- The one debug/REST surface planned for skills-service (via grpc-gateway, a later addition) is designed to be gated behind `ENABLE_DEBUG_HTTP` (off by default) with the same shared-secret check as the internal gRPC trust above — not built yet, noted here so the gate isn't forgotten when that surface is.

## Dependencies & static analysis

- **`gosec`** (security-focused Go linter) runs as part of `golangci-lint` in CI on every PR (`.golangci.yml` / `.github/workflows/backend-ci.yml`).
- **Dependabot** is configured (`.github/dependabot.yml`) for the Go module, both `package.json`s (frontend, notification-service), and GitHub Actions themselves — weekly, opens PRs for outdated/vulnerable dependencies.
- **CodeQL is deliberately not set up.** Its free tier only covers public repositories; this repo is currently private, and private-repo code scanning requires paid GitHub Advanced Security. Revisit this if the repo's visibility ever changes.

## Secrets

- Every `.env` file is gitignored (`**/.env` at the repo root covers every service's, including ones that don't exist yet) — only `.env.example` files with placeholder values are tracked.
- Nothing found committed that looks like a real credential (checked during this review).

## What a production deployment still needs before this is more than a learning build

In rough priority order: filling in `CORS_ALLOWED_ORIGINS` with the real Vercel URL once it exists (the mechanism is built, see above); extending RBAC beyond its current one-operation scope if more admin-only actions emerge; refresh-token rotation with reuse detection; reconsidering the rate limiter's fail-open behavior if abuse resistance ever matters more than availability for this project. The gateway↔service shared-secret header the plan's Phase 7 bullet raised is no longer on this list — see the Authorization section above for why `cmd/allinone`'s shape resolved it rather than deferring it.
