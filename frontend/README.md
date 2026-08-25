# frontend/

React 19 + Vite + TypeScript SPA. See `frontend/CLAUDE.md` for Node version, path aliases, and shadcn/ui conventions.

## What's actually wired up

The full synchronous + realtime feature set from `docs/PROJECT_OVERVIEW.md` now has a UI: register, log in, browse/add skills, browse/post job postings, view a job's matches, add a skill to your own profile, and a live-updating notification bell. Google OAuth is the one auth flow still API-only (see Known gaps).

- `src/lib/graphql-client.ts` — a ~20-line `fetch`-based GraphQL client, not Apollo/urql/`graphql-request`. Kept deliberately simple; TanStack Query already covers request state (loading/error/retry/caching by query key). Revisit only if a real need for normalized caching or optimistic updates shows up.
- `src/lib/auth-store.ts` — a Zustand store (`accessToken`, `userId`) persisted to `localStorage` via `zustand/middleware`'s `persist`. There's no refresh token in `AuthPayload` today (see `backend/internal/gateway/graph/schema.graphqls`), so this is a plain long-lived JWT in storage, not a rotation scheme — matches what the backend actually supports, not aspirational.
- `src/lib/jwt.ts` — `decodeJwtSubject(token)` decodes the JWT payload's `sub` claim client-side, **without verifying the signature** (verification already happened server-side against auth-service's JWKS; this is purely to recover the user ID). This exists because `login`'s `AuthPayload.userId` always comes back `""` by the gateway's own design (see `schema.resolvers.go`'s `Login` resolver comment) — `register` returns a real `userId` directly, `login` doesn't, so the client decodes it from the token instead.
- `src/lib/ws-client.ts` — a `graphql-ws` client for the `onNotification` subscription. Derives the `ws(s)://` URL from `VITE_API_URL` and sends the token via `connectionParams` (`Authorization: Bearer <token>`), matching `wsInitFunc`'s expectation on the gateway (see `backend/cmd/api-gateway/main.go`) — a WebSocket upgrade request can't carry a normal `Authorization` header, so this is the `connection_init`-time equivalent.
- `src/components/ProtectedRoute.tsx` — a `react-router` layout route that redirects to `/login` when there's no `accessToken` in the store.
- `src/components/Layout.tsx` — shared nav (Jobs/Skills/Profile, login/register or logout) wrapping every route; mounts `NotificationsBell` only when authenticated.
- `src/components/NotificationsBell.tsx` — a header dropdown backed by `useNotifications`, showing an unread-style count and the last 20 pushes in-memory (not persisted — a refresh clears it, there's no `notification_log` read API exposed to the gateway yet).
- `src/features/auth/` — `RegisterPage.tsx`, `LoginPage.tsx` (react-hook-form + Zod validation), `ProfilePage.tsx` (view profile + a small form for `addUserSkill`), `api.ts`.
- `src/features/skills/` — `SkillsPage.tsx` (list + create form), `api.ts`. `createSkill`/`skills` are unauthenticated per the schema (see `docs/DECISIONS.md` for why that's an accepted gap, not an oversight) — no login required to reach this page.
- `src/features/jobs/` — `JobsPage.tsx` (list, links to detail, "New job" button), `JobDetailPage.tsx` (required skills + live match list), `NewJobPage.tsx` (title/description + a required-skills checkbox list sourced from `skills`), `api.ts`. Also unauthenticated per the schema.
- `src/features/notifications/useNotifications.ts` — subscribes to `onNotification` for as long as there's an `accessToken`; tears down and reconnects on login/logout (the effect's dependency is `accessToken` itself).

Routes: `/` redirects to `/jobs`. `/register`, `/login`, `/skills`, `/jobs`, `/jobs/new`, `/jobs/:id` are public; `/profile` is protected.

## Running it against a live backend

```bash
cp .env.example .env.local   # VITE_API_URL, defaults to http://localhost:8080/query
npm install
npm run dev
```

For the full feature set (skills, job posting, matching, realtime push) you need all five backend pieces running with Kafka/RabbitMQ/Redis up — see `docs/RUNNING_LOCALLY.md`'s "All backend services locally" section. Register/login/profile alone only need `auth-service` + `users-service` + `api-gateway`; every service degrades gracefully without a DB, Kafka, or RabbitMQ, but the corresponding feature (login, matching, realtime push) just won't do anything until the dependency is there.

## Known gaps

- No test tooling configured (no Vitest) — there's real logic now (form validation, the JWT-decode workaround, protected-route redirects, the WebSocket reconnect-on-token-change effect) that isn't covered. See `docs/ARCHITECTURE.md`'s testing section.
- No Google OAuth UI yet, even though the backend supports it (`googleAuthUrl` / `googleOAuthCallback`).
- No token-expiry handling beyond a query/mutation failing and showing an inline error — there's no interceptor that proactively logs the user out or refreshes anything when the JWT expires (there's nothing to refresh — see the auth-store note above).
- Notifications are in-memory only, capped at the last 20, and gone on refresh — there's no `notificationLog` query on the gateway to backfill from `notifications.notification_log` (auth-service's RabbitMQ-driven log lives in notification-service's own schema; nothing currently exposes it to a GraphQL client).
