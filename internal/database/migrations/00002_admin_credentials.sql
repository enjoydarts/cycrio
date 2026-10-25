-- +goose Up
CREATE TABLE provider_credentials (
    provider text PRIMARY KEY CHECK (provider IN ('liquid','openrouter','openai','anthropic','resend')),
    state text NOT NULL DEFAULT 'not_configured' CHECK (state IN ('not_configured','configured','disabled')),
    version bigint NOT NULL DEFAULT 0 CHECK (version >= 0),
    updated_at timestamptz,
    CHECK ((state='not_configured' AND version=0 AND updated_at IS NULL) OR
           (state<>'not_configured' AND version>0 AND updated_at IS NOT NULL))
);
INSERT INTO provider_credentials (provider) VALUES ('liquid'),('openrouter'),('openai'),('anthropic'),('resend');

CREATE TABLE credential_versions (
    id uuid PRIMARY KEY,
    provider text NOT NULL REFERENCES provider_credentials(provider),
    version bigint NOT NULL CHECK (version > 0),
    key_id text NOT NULL CHECK (key_id <> ''),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) > 0),
    state text NOT NULL CHECK (state IN ('active','superseded','disabled')),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider,version)
);
CREATE UNIQUE INDEX credential_one_active ON credential_versions(provider) WHERE state='active';

CREATE TABLE credential_audit (
    id uuid PRIMARY KEY,
    actor_id uuid NOT NULL REFERENCES users(id),
    action text NOT NULL CHECK (action IN ('credential_created','credential_updated','credential_disabled')),
    provider text NOT NULL REFERENCES provider_credentials(provider),
    version bigint NOT NULL CHECK (version > 0),
    outcome text NOT NULL CHECK (outcome='success'),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX credential_audit_recent ON credential_audit(created_at DESC,id DESC);

-- +goose Down
DROP TABLE credential_audit;
DROP TABLE credential_versions;
DROP TABLE provider_credentials;
