-- +goose Up
-- jobs.job_matches is the matching worker's output: one row per (job,
-- user) the worker scored above the configured threshold when it
-- consumed that job's `job.posted` event (see docs/DECISIONS.md for the
-- exact algorithm/threshold chosen — simple skill-overlap scoring, not
-- ML, deliberately). The primary key on (job_id, user_id) is what makes
-- the worker's upsert idempotent: Kafka delivery is at-least-once, and
-- reprocessing the same job.posted event (or a redelivered one) must
-- update this row in place, never insert a duplicate.
CREATE TABLE IF NOT EXISTS jobs.job_matches (
    job_id     UUID NOT NULL REFERENCES jobs.jobs (id) ON DELETE CASCADE,
    user_id    UUID NOT NULL,
    score      DOUBLE PRECISION NOT NULL,
    matched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_jobs_job_matches_job_id ON jobs.job_matches (job_id);

-- +goose Down
DROP TABLE IF EXISTS jobs.job_matches;
