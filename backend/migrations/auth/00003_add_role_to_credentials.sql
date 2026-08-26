-- +goose Up
-- Adds RBAC to a schema that previously had no notion of it at all — see
-- docs/DECISIONS.md's RBAC notes for the full reasoning (why "role" lives
-- on the credential row rather than a separate roles table, why the
-- default is 'user', and how the first admin gets bootstrapped since
-- there is deliberately no self-service promotion path).
ALTER TABLE auth.credentials
    ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user';

-- +goose Down
ALTER TABLE auth.credentials
    DROP COLUMN IF EXISTS role;
