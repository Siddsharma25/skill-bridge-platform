-- +goose Up
-- jobs.jobs is the job postings table. jobs.job_matches and
-- jobs.user_skill_snapshot (the event-carried-state-transfer projection
-- fed by users-service's `user.skills.updated` Kafka event) are Phase 2
-- additions once Kafka exists — not created here. See docs/DECISIONS.md.
CREATE TABLE IF NOT EXISTS jobs.jobs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- jobs.job_required_skills is the join table between a job posting and the
-- skills-service Skill IDs it requires. No FK to skills.skills (different
-- schema/role, same isolation reasoning as users.user_skills) — the ID is
-- trusted as opaque and resolved for display by the gateway.
CREATE TABLE IF NOT EXISTS jobs.job_required_skills (
    job_id   UUID NOT NULL REFERENCES jobs.jobs (id) ON DELETE CASCADE,
    skill_id UUID NOT NULL,
    PRIMARY KEY (job_id, skill_id)
);

CREATE INDEX IF NOT EXISTS idx_jobs_job_required_skills_job_id ON jobs.job_required_skills (job_id);

-- +goose Down
DROP TABLE IF EXISTS jobs.job_required_skills;
DROP TABLE IF EXISTS jobs.jobs;
