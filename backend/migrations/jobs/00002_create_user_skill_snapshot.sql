-- +goose Up
-- jobs.user_skill_snapshot is jobs-service's own event-carried-state-
-- transfer projection of every user's current skill list, fed entirely by
-- consuming users-service's `user.skills.updated` Kafka event (see
-- docs/DECISIONS.md's "why jobs-service doesn't just query
-- users-service's database" — jobs_service's Postgres role genuinely
-- cannot read users.* even though it's the same physical database). This
-- is what lets the matching worker score candidates against a job's
-- required skills without ever calling users-service directly, and stay
-- fully functional even if users-service is down.
--
-- The consumer (internal/jobs/snapshot.go) treats each event as the
-- user's *entire* current list, not a delta, and replaces this user's
-- rows atomically (delete-then-insert in one transaction) on every
-- message — so repeated/out-of-order delivery can never leave a
-- duplicate row or a stale skill the user no longer has.
CREATE TABLE IF NOT EXISTS jobs.user_skill_snapshot (
    user_id     UUID NOT NULL,
    skill_id    UUID NOT NULL,
    proficiency TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, skill_id)
);

-- The matching worker's core query is "which users have at least one of
-- these skill_ids" (see internal/jobs/matcher.go's CandidatesForSkills) —
-- an index on skill_id is what makes that a lookup instead of a full
-- table scan as the snapshot grows.
CREATE INDEX IF NOT EXISTS idx_jobs_user_skill_snapshot_skill_id ON jobs.user_skill_snapshot (skill_id);

-- +goose Down
DROP TABLE IF EXISTS jobs.user_skill_snapshot;
