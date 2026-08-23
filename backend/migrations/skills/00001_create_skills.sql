-- +goose Up
-- skills.skills is the whole taxonomy skills-service owns in this phase.
-- name is unique so CreateSkill can't silently accumulate duplicates —
-- callers get a clear AlreadyExists instead.
CREATE TABLE IF NOT EXISTS skills.skills (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    category   TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_skills_skills_name ON skills.skills (name);

-- +goose Down
DROP TABLE IF EXISTS skills.skills;
