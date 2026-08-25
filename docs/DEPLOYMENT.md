# Deployment Checklist

Everything that can be built and verified from inside this repo is done — `backend/cmd/allinone`, `backend/Dockerfile.allinone`, `render.yaml`, `frontend/vercel.json`, and CORS on api-gateway all exist and were tested locally end-to-end (see `docs/DECISIONS.md`'s Phase 7 notes for the full verification log). What's left below requires creating and connecting real third-party accounts — nothing further can be done without you.

Production is a deliberately different, much simpler shape than the local/kind learning architecture: **one Render web service** running `backend/cmd/allinone` (all four backend services + the API gateway in a single process) and **one Vercel static deploy** of `frontend/`. Kafka/RabbitMQ don't run in production at all — job matching, the notification log, and realtime WebSocket push are local/kind-only features; the synchronous GraphQL surface (register, login, createSkill, createJob, myProfile, addUserSkill) works identically either way.

## 1. Create a Supabase project

- Free tier is fine. Note: free Supabase projects auto-pause after 7 days of inactivity and need a manual dashboard restore.
- Run `backend/migrations/000_bootstrap.sql` against it with an **admin** connection (not the pooler) — this creates the four schemas (`auth`, `skills`, `users`, `jobs`) and their scoped roles.
- Set a real password for each of the four roles (the bootstrap script uses a placeholder).
- Run `make migrate-up` (from `backend/`, with `DATABASE_URL` pointed at each schema in turn, or however you've scripted it per prior phases) to apply each service's own goose migrations.
- Get the **Supavisor pooler** connection string for each role (transaction mode, port 6543) — GORM is already configured for `PreferSimpleProtocol` to work with this pooler mode (see `docs/DECISIONS.md`).

## 2. Create a Render account and deploy the backend

- Connect this GitHub repo to Render.
- Apply `render.yaml` (repo root) as a **Blueprint** — it points at `backend/Dockerfile.allinone` via `rootDir: backend`.
- Fill in every env var the Blueprint marks `sync: false` (Render's dashboard prompts for these):
  - `AUTH_DATABASE_URL`, `SKILLS_DATABASE_URL`, `USERS_DATABASE_URL`, `JOBS_DATABASE_URL` — the four pooler connection strings from step 1.
  - `JWT_PRIVATE_KEY_PEM` — generate a real RS256 keypair (`openssl genrsa 2048`), reformat to one line with literal `\n` escapes. **Don't skip this** — leaving it unset works for a quick smoke test, but every restart/redeploy invalidates every outstanding token if it's unset (auth-service falls back to generating a throwaway key each time).
  - Optional: `GOOGLE_OAUTH_CLIENT_ID` / `GOOGLE_OAUTH_CLIENT_SECRET` / `GOOGLE_OAUTH_REDIRECT_URL` (see step 5), `REDIS_URL` (see the note below).
- Note the resulting service URL (`https://<your-service>.onrender.com`).

**About Redis in production**: `render.yaml` leaves `REDIS_URL` unset by default. Rate limiting and caching are already confirmed to degrade gracefully (fail open / no-op) with it unset — verified live against the actual `allinone` binary, not just assumed. Render's own managed Redis is paid-only, and this Blueprint doesn't provision a third-party one. Leave it unset unless you specifically want rate limiting/caching enforced in production and are willing to add a Redis add-on yourself.

## 3. Create a Vercel account and deploy the frontend

- Import this repo, set the project's **Root Directory to `frontend`** (a dashboard setting — `frontend/vercel.json` handles build output and SPA routing once this is set correctly).
- Set `VITE_API_URL` to `https://<your-render-service>.onrender.com/query` (the URL from step 2).

## 4. Close the loop: CORS

- Back in Render, set `CORS_ALLOWED_ORIGINS` to the real Vercel URL from step 3 (comma-separated if you also want `http://localhost:5173` to keep working for local dev against the live backend).
- Without this, the deployed frontend's requests will be silently blocked by the browser's own CORS enforcement — the backend doesn't reject them server-side, but the browser won't let the response through.

## 5. Optional: Google OAuth

- Only needed if you want "Sign in with Google" live, not just password auth.
- Create a Google Cloud OAuth client (Web application type). It needs a **stable domain** — not a Vercel preview URL, which changes per-deploy and won't work as a registered redirect URI.
- Set `GOOGLE_OAUTH_REDIRECT_URL` on Render to the deployed frontend's callback route.

## What you'll have after this

A live, publicly reachable job/skill-matching app — register, login, post jobs, list skills, get matched, all through the deployed GraphQL API and (once the frontend is actually built out — see `docs/PROJECT_OVERVIEW.md`'s "where this is headed") a real UI. Async flows (Kafka matching triggers, RabbitMQ notifications, WebSocket push) stay local/kind-only; that gap is intentional, not a bug — see `docs/DECISIONS.md`'s "Production deployment: a single allinone binary" section for the full reasoning.
