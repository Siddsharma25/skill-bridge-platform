-- +goose Up
-- auth.oauth_identities links a third-party identity (today: only Google)
-- to one auth.credentials row. A credential can have zero or more linked
-- identities (password-only, Google-only, or both); an identity always
-- belongs to exactly one credential, enforced by the FK below and by
-- account-linking happening on this table's INSERT, never a second
-- credentials row for the same person — see docs/DECISIONS.md for the
-- exact linking rule (existing identity wins outright; else an existing
-- credential for the same email is linked to; else both rows are created
-- fresh).
--
-- (provider, provider_subject) is the natural key Google guarantees is
-- stable and unique per Google Account — never re-used, and (unlike email)
-- never changes if the user later renames their email. email is stored
-- here too, redundantly, purely for observability/debugging (support: "why
-- did this identity link to that account") — it is never itself treated as
-- the uniqueness key for this table, only credentials.email is.
CREATE TABLE IF NOT EXISTS auth.oauth_identities (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES auth.credentials (id) ON DELETE CASCADE,
    provider         TEXT NOT NULL,
    provider_subject TEXT NOT NULL,
    email            TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_subject)
);

CREATE INDEX IF NOT EXISTS idx_auth_oauth_identities_user_id ON auth.oauth_identities (user_id);

-- +goose Down
DROP TABLE IF EXISTS auth.oauth_identities;
