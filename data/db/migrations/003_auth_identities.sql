-- Supports OAuth-only users and links external provider identities.
BEGIN;

ALTER TABLE app_user ALTER COLUMN password_hash DROP NOT NULL;

CREATE TABLE auth_identity (
    user_id             uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    provider            text NOT NULL,
    provider_subject    text NOT NULL,
    email               text NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, provider_subject),
    UNIQUE (user_id, provider)
);

COMMIT;
