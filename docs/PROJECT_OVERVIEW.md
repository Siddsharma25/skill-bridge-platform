# What This Project Is

Skill Bridge Platform is a **job/skill-gap matching platform** — the kind of product a job board or internal talent-mobility tool would run. A candidate lists the skills they have; an employer posts a job listing the skills it requires; the platform matches the two automatically and notifies the candidate when a match happens.

It's built as a hands-on learning vehicle first and a product second: every architectural choice (microservices, gRPC, Kafka, RabbitMQ, Kubernetes, GraphQL, WebSockets, NestJS) was chosen for the practice it gives, not because a job board strictly needs all of it. `docs/DECISIONS.md` explains the *why* behind the architecture; this document explains the *what* — what the product actually does, in plain terms.

## The core functionality

1. **Account creation.** A person registers with an email/password, or signs in with Google. Either way they end up with the same kind of session (a signed access token).
2. **Skill taxonomy.** There's a shared list of skills (e.g. "Go", "Kubernetes", "Project Management") with categories — anyone can add a new skill to this shared catalog.
3. **Candidate profile.** A logged-in user has a profile (display name, bio) and a list of skills they claim to have, each with a proficiency level. The profile is created automatically the first time it's touched.
4. **Job postings.** Anyone can post a job — a title, a description, and the list of skills it requires (pulled from the shared taxonomy).
5. **Automatic matching.** When a job is posted, the platform scores every candidate against that job's required skills (a simple overlap score, not machine learning) and records a match for anyone who clears the threshold.
6. **Notifications.** Two independent things happen when relevant events occur:
   - A **welcome email** gets logged (not actually sent — see below) when someone registers.
   - A **live, real-time push** reaches a candidate's browser the moment they get matched to a job, if they're connected — no page refresh, no polling.

## What's real and what's simulated

- **No real email is ever sent.** The "welcome email" flow logs what it would have sent and records it in a database table — this project deliberately doesn't integrate a real email provider, since that's incidental to the messaging/NestJS practice it's there for.
- **The matching algorithm is simple skill-overlap scoring, not ML.** Sophisticated matching isn't the point of this project.
- **There's no concept of roles yet** — anyone can post a job or add a skill, authenticated or not. A real product would need employer/candidate/admin roles; this project deliberately hasn't built that yet (see `docs/SECURITY.md`).

## How to actually use it right now

There's no user-facing web app yet — `frontend/` is still an unwired Vite/React scaffold. Everything above is exercised through the backend's **GraphQL API** directly (via a GraphQL client, `curl`, or the Playground the gateway serves in dev). The relevant operations:

- `register(email, password)` / `login(email, password)` / `googleAuthUrl` + `googleOAuthCallback(code)` — mutations
- `skills` (query, list) / `createSkill(name, category)` (mutation)
- `jobs` (query, list) / `job(id)` (query) / `createJob(title, description, requiredSkillIds)` (mutation) — `Job.requiredSkills` and `Job.matches` resolve automatically
- `myProfile` (query, requires auth) / `updateProfile(displayName, bio)` / `addUserSkill(skillId, proficiency)` — mutations, all require a valid bearer token from `login`
- `onNotification` — a GraphQL subscription over WebSocket; open it with a valid token and it pushes a live message when you get matched to a job

See `backend/cmd/api-gateway/README.md` for the exact schema and `docs/DECISIONS.md` for how each piece is wired underneath (which service owns what, which events drive what).

## Where this is headed (not yet built)

A real frontend that actually calls this API, an authorization/role system, refresh-token rotation, and — per `docs/DEPLOYMENT.md` — a live deployment once the necessary third-party accounts (Supabase, Render, Vercel) exist.
