# frontend/

React 19 + Vite + TypeScript SPA. See `frontend/CLAUDE.md` for Node version, path aliases, and shadcn/ui conventions.

## What's actually wired up

**Auth only, so far**: register, log in, and view your own profile. Everything else described in `docs/PROJECT_OVERVIEW.md` (skills, job postings, matching, realtime notifications) has no UI yet — see that doc for how to exercise those directly via the GraphQL API in the meantime.

- `src/lib/graphql-client.ts` — a ~20-line `fetch`-based GraphQL client, not Apollo/urql/`graphql-request`. There's exactly three operations today; a full client's caching/normalization machinery isn't earning its weight yet. Revisit this choice once the feature surface grows past auth.
- `src/lib/auth-store.ts` — a Zustand store (`accessToken`, `userId`) persisted to `localStorage` via `zustand/middleware`'s `persist`. There's no refresh token in `AuthPayload` today (see `backend/internal/gateway/graph/schema.graphqls`), so this is a plain long-lived JWT in storage, not a rotation scheme — matches what the backend actually supports, not aspirational.
- `src/lib/jwt.ts` — `decodeJwtSubject(token)` decodes the JWT payload's `sub` claim client-side, **without verifying the signature** (verification already happened server-side against auth-service's JWKS; this is purely to recover the user ID). This exists because `login`'s `AuthPayload.userId` always comes back `""` by the gateway's own design (see `schema.resolvers.go`'s `Login` resolver comment) — `register` returns a real `userId` directly, `login` doesn't, so the client decodes it from the token instead. `RegisterPage` and `LoginPage` both go through the same store-set path for consistency.
- `src/components/ProtectedRoute.tsx` — a `react-router` layout route that redirects to `/login` when there's no `accessToken` in the store.
- `src/features/auth/` — `RegisterPage.tsx`, `LoginPage.tsx` (react-hook-form + Zod validation, a TanStack Query `useMutation`), `ProfilePage.tsx` (a TanStack Query `useQuery` against `myProfile`, gated behind `ProtectedRoute`), and `api.ts` (the three GraphQL documents + typed fetch wrappers).

Routes: `/` redirects to `/login`, `/register`, `/login`, and `/profile` (protected).

## Running it against a live backend

```bash
cp .env.example .env.local   # VITE_API_URL, defaults to http://localhost:8080/query
npm install
npm run dev
```

You need `auth-service` + `users-service` + `api-gateway` running for register/login/profile to actually work — see `docs/RUNNING_LOCALLY.md`'s "All backend services locally" section. Every service degrades gracefully without a DB, but register/login will just fail with a GraphQL error until they have one.

## Known gaps

- No test tooling configured (no Vitest) — the auth pages have real logic now (form validation, the JWT-decode workaround, redirect-on-missing-token) that isn't covered. See `docs/ARCHITECTURE.md`'s testing section.
- No Google OAuth UI yet, even though the backend supports it (`googleAuthUrl` / `googleOAuthCallback`).
- No token-expiry handling beyond `myProfile` failing and showing an error — there's no interceptor that proactively logs the user out when the JWT expires.
