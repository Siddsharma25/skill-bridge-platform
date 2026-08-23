-- 000_bootstrap.sql
--
-- Run ONCE against Supabase with an admin connection (the "postgres" role,
-- direct connection — NOT through the Supavisor pooler, since role
-- creation and GRANTs are session/administrative operations). This is not
-- a goose migration deliberately: goose migrations run per-service through
-- each service's own scoped role, and that role doesn't have permission to
-- create other roles or schemas — bootstrapping the schemas/roles is what
-- makes that restriction possible in the first place.
--
-- This implements "database-per-service" on one shared Postgres instance:
-- every service gets its own schema and its own login role, scoped via
-- GRANT so that (for example) jobs-service's role genuinely cannot read
-- users.* or auth.* tables even though they live in the same physical
-- database. See docs/DECISIONS.md for why this matters (it's what forces
-- jobs-service to consume `user.skills.updated` off Kafka instead of just
-- querying users-service's tables directly).
--
-- Verify the Supavisor custom-role username format
-- (`<role>.<project-ref>`) against the live project before relying on it —
-- noted here per the plan's Phase 1a guidance, not yet verified against a
-- live project since none exists at the time this file was written.

-- === auth schema + role ===================================================
-- Owns: auth.credentials (this phase), auth.oauth_identities and
-- auth.refresh_tokens (later phases per docs/DECISIONS.md).

CREATE SCHEMA IF NOT EXISTS auth;

DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'auth_service') THEN
    CREATE ROLE auth_service LOGIN PASSWORD 'CHANGE_ME_IN_SUPABASE_DASHBOARD';
  END IF;
END
$$;

GRANT USAGE ON SCHEMA auth TO auth_service;
GRANT CREATE ON SCHEMA auth TO auth_service;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA auth TO auth_service;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA auth TO auth_service;
ALTER DEFAULT PRIVILEGES IN SCHEMA auth
  GRANT ALL PRIVILEGES ON TABLES TO auth_service;
ALTER DEFAULT PRIVILEGES IN SCHEMA auth
  GRANT ALL PRIVILEGES ON SEQUENCES TO auth_service;

-- === users / skills / jobs schemas + roles ================================
-- Created here (not deferred to Phase 1b) so the bootstrap script is
-- idempotent and complete on day one — running it once now means Phase 1b
-- doesn't need a second admin-connection migration just to add schemas.
-- The services themselves (and their goose migrations) don't exist until
-- Phase 1b; only the isolation boundary is established here.

CREATE SCHEMA IF NOT EXISTS users;
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'users_service') THEN
    CREATE ROLE users_service LOGIN PASSWORD 'CHANGE_ME_IN_SUPABASE_DASHBOARD';
  END IF;
END
$$;
GRANT USAGE ON SCHEMA users TO users_service;
GRANT CREATE ON SCHEMA users TO users_service;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA users TO users_service;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA users TO users_service;
ALTER DEFAULT PRIVILEGES IN SCHEMA users
  GRANT ALL PRIVILEGES ON TABLES TO users_service;
ALTER DEFAULT PRIVILEGES IN SCHEMA users
  GRANT ALL PRIVILEGES ON SEQUENCES TO users_service;

CREATE SCHEMA IF NOT EXISTS skills;
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'skills_service') THEN
    CREATE ROLE skills_service LOGIN PASSWORD 'CHANGE_ME_IN_SUPABASE_DASHBOARD';
  END IF;
END
$$;
GRANT USAGE ON SCHEMA skills TO skills_service;
GRANT CREATE ON SCHEMA skills TO skills_service;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA skills TO skills_service;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA skills TO skills_service;
ALTER DEFAULT PRIVILEGES IN SCHEMA skills
  GRANT ALL PRIVILEGES ON TABLES TO skills_service;
ALTER DEFAULT PRIVILEGES IN SCHEMA skills
  GRANT ALL PRIVILEGES ON SEQUENCES TO skills_service;

CREATE SCHEMA IF NOT EXISTS jobs;
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'jobs_service') THEN
    CREATE ROLE jobs_service LOGIN PASSWORD 'CHANGE_ME_IN_SUPABASE_DASHBOARD';
  END IF;
END
$$;
GRANT USAGE ON SCHEMA jobs TO jobs_service;
GRANT CREATE ON SCHEMA jobs TO jobs_service;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA jobs TO jobs_service;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA jobs TO jobs_service;
ALTER DEFAULT PRIVILEGES IN SCHEMA jobs
  GRANT ALL PRIVILEGES ON TABLES TO jobs_service;
ALTER DEFAULT PRIVILEGES IN SCHEMA jobs
  GRANT ALL PRIVILEGES ON SEQUENCES TO jobs_service;

-- === notifications schema + role ==========================================
-- notification-service (NestJS, Phase 3) uses plain `pg`, not GORM/goose,
-- but the schema/role isolation model is identical.

CREATE SCHEMA IF NOT EXISTS notifications;
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'notification_service') THEN
    CREATE ROLE notification_service LOGIN PASSWORD 'CHANGE_ME_IN_SUPABASE_DASHBOARD';
  END IF;
END
$$;
GRANT USAGE ON SCHEMA notifications TO notification_service;
GRANT CREATE ON SCHEMA notifications TO notification_service;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA notifications TO notification_service;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA notifications TO notification_service;
ALTER DEFAULT PRIVILEGES IN SCHEMA notifications
  GRANT ALL PRIVILEGES ON TABLES TO notification_service;
ALTER DEFAULT PRIVILEGES IN SCHEMA notifications
  GRANT ALL PRIVILEGES ON SEQUENCES TO notification_service;

-- NOTE: passwords above are placeholders. Set real per-role passwords via
-- the Supabase dashboard (or ALTER ROLE ... PASSWORD) and put the resulting
-- Supavisor connection string in each service's DATABASE_URL — never
-- commit the real password into this file or into git history.
