-- +goose Up
-- users.profiles rows are created lazily by GetProfile/UpdateProfile the
-- first time a user_id is touched (there is no Kafka `user.registered`
-- consumer yet — see docs/DECISIONS.md's Phase 1b -> 2 migration note), so
-- user_id has no foreign key to auth.credentials: users-service's role
-- cannot read the auth schema at all (schema-per-service isolation), and
-- there is nothing to backfill from anyway at creation time.
CREATE TABLE IF NOT EXISTS users.profiles (
    user_id      UUID PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    bio          TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- users.user_skills is a user's claimed skills, referencing
-- skills-service's Skill IDs by value only (no cross-schema FK is
-- possible or attempted here, same isolation reasoning as above).
-- Composite primary key enforces one proficiency row per (user, skill)
-- pair, which is what AddUserSkill's upsert relies on.
CREATE TABLE IF NOT EXISTS users.user_skills (
    user_id     UUID NOT NULL,
    skill_id    UUID NOT NULL,
    proficiency TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, skill_id)
);

CREATE INDEX IF NOT EXISTS idx_users_user_skills_user_id ON users.user_skills (user_id);

-- +goose Down
DROP TABLE IF EXISTS users.user_skills;
DROP TABLE IF EXISTS users.profiles;
