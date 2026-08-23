-- +goose Up
-- auth.credentials is the only table auth-service owns in this phase.
-- Password hashing (bcrypt) happens in application code, never in SQL —
-- the column just stores the resulting hash string.
CREATE TABLE IF NOT EXISTS auth.credentials (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_auth_credentials_email ON auth.credentials (email);

-- +goose Down
DROP TABLE IF EXISTS auth.credentials;
