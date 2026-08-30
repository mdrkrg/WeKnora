CREATE TABLE IF NOT EXISTS user_external_identities (
    id           BIGSERIAL PRIMARY KEY,
    user_id      VARCHAR(36) NOT NULL,
    authority    VARCHAR(512) NOT NULL,
    external_uid VARCHAR(512) NOT NULL,
    resolved_via VARCHAR(32) NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_user_external_identities_authority_uid UNIQUE (authority, external_uid)
);

CREATE INDEX IF NOT EXISTS idx_user_external_identities_user_id ON user_external_identities (user_id);
