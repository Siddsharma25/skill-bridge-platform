# frontend/

React 19 + Vite + TypeScript SPA. See `frontend/CLAUDE.md` for Node version, path aliases, and shadcn/ui conventions.

## What's actually wired up

The full synchronous + realtime feature set from `docs/PROJECT_OVERVIEW.md` now has a UI: register, log in, browse/add skills, browse/post job postings, view a job's matches, add a skill to your own profile, and a live-updating notification bell. Google OAuth is the one auth flow still API-only (see Known gaps).

- `src/lib/graphql-client.ts` — a ~20-line `fetch`-based GraphQL client, not Apollo/urql/`graphql-request`. Kept deliberately simple; TanStack Query already covers request state (loading/error/retry/caching by query key). Revisit only if a real need for normalized caching or optimistic updates shows up.
- `src/lib/auth-store.ts` — a Zustand store (`accessToken`, `userId`, `role`) persisted to `localStorage` via `zustand/middleware`'s `persist`. There's no refresh token in `AuthPayload` today (see `backend/internal/gateway/graph/schema.graphqls`), so this is a plain long-lived JWT in storage, not a rotation scheme — matches what the backend actually supports, not aspirational. `role` is RBAC's client-side half (see below) — a UI convenience only, never the actual authorization boundary.
- `src/lib/jwt.ts` — `decodeJwtSubject(token)`/`decodeJwtRole(token)` decode the JWT payload's `sub`/`role` claims client-side, **without verifying the signature** (verification already happened server-side against auth-service's JWKS; this is purely to recover values `AuthPayload` doesn't return on the wire). This exists because `login`'s `AuthPayload.userId` always comes back `""` by the gateway's own design (see `schema.resolvers.go`'s `Login` resolver comment) — `register` returns a real `userId` directly, `login` doesn't, so the client decodes it from the token instead; neither mutation returns `role` at all, so both flows always decode it from the token.
- `src/lib/ws-client.ts` — a `graphql-ws` client for the `onNotification` subscription. Derives the `ws(s)://` URL from `VITE_API_URL` and sends the token via `connectionParams` (`Authorization: Bearer <token>`), matching `wsInitFunc`'s expectation on the gateway (see `backend/cmd/api-gateway/main.go`) — a WebSocket upgrade request can't carry a normal `Authorization` header, so this is the `connection_init`-time equivalent.
- `src/components/ProtectedRoute.tsx` — a `react-router` layout route that redirects to `/login` when there's no `accessToken` in the store.
- `src/components/Layout.tsx` — shared nav (Jobs/Skills/Profile, login/register or logout) wrapping every route; mounts `NotificationsBell` only when authenticated.
- `src/components/NotificationsBell.tsx` — a header dropdown backed by `useNotifications`, showing an unread-style count and up to the last 20 notifications. Seeded on mount from `notificationHistory` (a Redis Stream on the backend — see `docs/DECISIONS.md`'s Redis Streams notes), then kept live by the `onNotification` subscription, so a page refresh no longer loses everything that happened before the tab was open.
- `src/features/auth/` — `RegisterPage.tsx`, `LoginPage.tsx` (react-hook-form + Zod validation), `ProfilePage.tsx` (view profile + a small form for `addUserSkill`), `api.ts`.
- `src/features/skills/` — `SkillsPage.tsx` (list + admin-gated create form), `api.ts`. `skills` (the list) stays unauthenticated; `createSkill` now requires an admin bearer token (RBAC — see `docs/DECISIONS.md`) — the page renders the "Add a skill" form only when `role === "admin"`, and a plain explanation otherwise. This client-side check is UX only; `requireAdmin` on the backend is the actual boundary.
- `src/features/jobs/` — `JobsPage.tsx` (list, links to detail, "New job" button), `JobDetailPage.tsx` (required skills + live match list), `NewJobPage.tsx` (title/description + a required-skills checkbox list sourced from `skills`), `api.ts`. Also unauthenticated per the schema.
- `src/features/notifications/useNotifications.ts` — fetches `notificationHistory` once on mount to backfill, then subscribes to `onNotification` for as long as there's an `accessToken`; tears down and reconnects on login/logout (the effect's dependency is `accessToken` itself). Live pushes are deduped against the backfilled list by `id`.

Routes: `/` redirects to `/jobs`. `/register`, `/login`, `/skills`, `/jobs`, `/jobs/new`, `/jobs/:id` are public; `/profile` is protected.

## Browser storage: one deliberate example of each

Four genuinely different browser storage mechanisms are used here, each picked for what it's actually good at rather than defaulting to one everywhere:

- **`localStorage`** (`src/lib/auth-store.ts`) — the JWT, since it needs to survive closing the tab/browser entirely. Origin-scoped, persists until explicitly cleared.
- **`sessionStorage`** (`src/lib/storage/recentlyViewedJobs.ts`) — a "recently viewed jobs" list on `JobsPage`, populated from `JobDetailPage`. Tab-scoped and gone when the tab closes: open the app in two tabs and each gets its own list, which is the point — this is "what did I browse just now," not something worth remembering across visits.
- **Cookies** (`src/lib/storage/themeCookie.ts`, `src/components/ThemeToggle.tsx`) — the light/dark theme preference. Applied in `main.tsx` before the first render (no flash of the wrong theme), falling back to `prefers-color-scheme` if no cookie is set yet. Plain `document.cookie`, no library — this app has no server to read it, but a real SSR app would, which is the actual reason cookies exist as a distinct mechanism from the other three (they ride along with every request to the same origin).
- **IndexedDB** (`src/lib/storage/jobDraftDb.ts`) — autosaves the `NewJobPage` form as you type (debounced 400ms) and restores it if you navigate away and come back before submitting; cleared on successful submit. Raw `indexedDB` API, no `idb`/Dexie wrapper, so the actual mechanism (a versioned async database, object stores created only in `onupgradeneeded`, one transaction per read/write) is visible rather than hidden — the right choice here since a draft is a structured multi-field record, not a single string the other two `*Storage` APIs would need to serialize by hand anyway.

## RBAC (roles)

Two roles, `user` and `admin` (see `docs/DECISIONS.md`'s RBAC notes for the full backend-side design). There's no self-service way to become an admin from this UI — an account is promoted directly via SQL (`UPDATE auth.credentials SET role = 'admin' WHERE email = '...'`), then that person logs in again to get a token carrying the new role (roles are baked into the token at issuance, not re-checked live — logging in again is what picks up a role change). `SkillsPage`'s "Add a skill" form is the one place this app's UI currently reacts to role.

## Accessibility (WCAG)

A real pass across the existing pages, not a separate demo — see `docs/DECISIONS.md`'s Accessibility section for the full list and the WCAG success criteria each change addresses. In brief: a skip-to-content link (`Layout.tsx`), `aria-current="page"` on the active nav link, a fully ARIA-annotated + keyboard-closable notification bell with a live region (`NotificationsBell.tsx`), app-wide `prefers-reduced-motion` support (`index.css`), and a real page `<title>`. Honestly documented gaps: no automated accessibility testing (axe/Lighthouse) is wired in, and no actual screen-reader session confirmed the experience — everything here was reasoned through against the spec, not machine- or human-verified end-to-end.

## Error tracking (Sentry)

`src/lib/sentry.ts`'s `initSentry()` (called from `main.tsx`, before the first render) is a no-op unless `VITE_SENTRY_DSN` is set — same degrade-gracefully convention as everything else in `.env.example`. This closes a real gap: production (Vercel) had zero error visibility before this — an uncaught render error just showed a blank page with nothing to investigate. `App.tsx` wraps the whole route tree in `Sentry.ErrorBoundary` (a minimal "Something went wrong" fallback with a reload button); it's itself a no-op pass-through when Sentry was never initialized.

`event.request.data` (any captured request body) is stripped in `beforeSend` — defense in depth alongside the backend's own body-capture opt-out (see `docs/SECURITY.md`). Performance tracing (`tracesSampleRate`) defaults to 0 (off), matching the backend's `SENTRY_TRACES_SAMPLE_RATE` default, so this doesn't silently burn Sentry's free-tier quota.

Source map upload (`vite.config.ts`, `@sentry/vite-plugin`) is a separate, build-time-only opt-in gated on `SENTRY_AUTH_TOKEN`/`SENTRY_ORG`/`SENTRY_PROJECT` all being set — **never** as a `VITE_`-prefixed var, since those get inlined into the shipped bundle (see `docs/SECURITY.md`). `build.sourcemap` is `"hidden"` only when that upload is actually configured (generates `.map` files for the plugin to upload and then delete, without a `sourceMappingURL` comment in the shipped JS); otherwise it stays off entirely rather than shipping unreferenced map files for nothing.

## Testing (Vitest + React Testing Library)

```bash
npm test              # run once
npm run test:watch    # watch mode
npm run test:coverage # generates frontend/coverage/ (open coverage/index.html)
```

**Vitest**, not Jest — this project already runs on Vite, and Vitest reuses Vite's own config/transform pipeline directly (see `vite.config.ts`'s `test` block: one file configures both the dev server and the test runner, no separate Jest/Babel config to keep in sync). It's also the current default recommendation for a Vite-based React app for exactly that reason. Paired with **React Testing Library** (renders a component and queries it the way a user would — by visible label/role text, not internal implementation details) + `@testing-library/user-event` for realistic typing/clicking, and `@vitest/coverage-v8` for coverage (V8's own native instrumentation, no separate Istanbul instrumentation step).

Real tests exist today, not a scaffold: `lib/jwt.test.ts` (pure-function claim decoding), `lib/storage/recentlyViewedJobs.test.ts` (dedupe/cap/ordering logic), and `features/auth/LoginPage.test.tsx` (a full render + fill-in + submit + assert pass, mocking only the network call) — 11 tests, all passing. Coverage is intentionally still low project-wide (these three files are the actual point, not a mandate to reach 100% everywhere) — see the coverage report for exactly what's covered.

One real bug this surfaced, not a test-only workaround: submitting `LoginPage`/`RegisterPage` with `type="email"` inputs was letting the *browser's* native HTML5 validation intercept the submit before React Hook Form/Zod ever ran — inconsistent across browsers and less informative than the app's own Zod messages. Both forms now set `noValidate`, making Zod's validation authoritative.

`src/test/setup.ts` registers RTL's cleanup in an explicit `afterEach` (this config doesn't use Vitest's `globals: true` — every test file imports `describe`/`it`/`expect` explicitly, matching this codebase's no-ambient-magic style elsewhere — so RTL's own auto-cleanup-via-global-afterEach never fires on its own). `src/test/makeToken.ts` is a shared helper building a real three-part JWT string (via `btoa`, not Node's `Buffer` — this is a browser-only tsconfig) for any test needing a real token shape.

## Running it against a live backend

```bash
cp .env.example .env.local   # VITE_API_URL, defaults to http://localhost:8080/query
npm install
npm run dev
```

For the full feature set (skills, job posting, matching, realtime push) you need all five backend pieces running with Kafka/RabbitMQ/Redis up — see `docs/RUNNING_LOCALLY.md`'s "All backend services locally" section. Register/login/profile alone only need `auth-service` + `users-service` + `api-gateway`; every service degrades gracefully without a DB, Kafka, or RabbitMQ, but the corresponding feature (login, matching, realtime push) just won't do anything until the dependency is there.

## Known gaps

- Vitest is now configured (see Testing above), but only 3 files have real tests — `ProfilePage`, `SkillsPage`, `NewJobPage`'s draft-autosave logic, `ProtectedRoute`, and every other component/hook are still untested. See `docs/ARCHITECTURE.md`'s testing section.
- No Google OAuth UI yet, even though the backend supports it (`googleAuthUrl` / `googleOAuthCallback`).
- No token-expiry handling beyond a query/mutation failing and showing an inline error — there's no interceptor that proactively logs the user out or refreshes anything when the JWT expires (there's nothing to refresh — see the auth-store note above).
- Notification history is Redis-backed (a Stream, capped at 50 entries server-side), not the durable Postgres `notifications.notification_log` notification-service owns — if Redis is flushed/restarted, history is gone even though notification-service's own log of the same events survives. Nothing currently exposes `notification_log` to a GraphQL client; the Redis Stream is a separate, smaller "recent activity" cache, not a replacement for that durable log.
