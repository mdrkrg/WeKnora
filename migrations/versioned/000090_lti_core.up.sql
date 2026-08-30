-- LTI 1.3 tool-side core tables (registrations, tool keys, tickets).

CREATE TABLE IF NOT EXISTS lti_registrations (
    id                BIGSERIAL PRIMARY KEY,
    issuer            VARCHAR(512) NOT NULL,
    client_id         VARCHAR(256) NOT NULL,
    deployment_ids    TEXT NOT NULL DEFAULT '',
    auth_endpoint     TEXT NOT NULL DEFAULT '',
    jwks_uri          TEXT NOT NULL DEFAULT '',
    public_keyset     TEXT NOT NULL DEFAULT '',
    keyset_fetched_at TIMESTAMPTZ,
    directory_claim   VARCHAR(128) NOT NULL DEFAULT '',
    enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_lti_registrations_iss_client UNIQUE (issuer, client_id)
);

CREATE TABLE IF NOT EXISTS lti_tool_keys (
    k_id        VARCHAR(128) PRIMARY KEY,
    private_key TEXT NOT NULL,
    public_jwk  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS lti_tickets (
    token_hash  VARCHAR(64) PRIMARY KEY,
    user_id     VARCHAR(36) NOT NULL,
    context_id  VARCHAR(256) NOT NULL DEFAULT '',
    roles       TEXT NOT NULL DEFAULT '',
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_lti_tickets_user_id ON lti_tickets (user_id);
CREATE INDEX IF NOT EXISTS idx_lti_tickets_expires_at ON lti_tickets (expires_at);
CREATE INDEX IF NOT EXISTS idx_lti_tickets_consumed_at ON lti_tickets (consumed_at);
