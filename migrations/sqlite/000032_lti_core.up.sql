-- LTI 1.3 tool-side core tables (registrations, tool keys, tickets).

CREATE TABLE IF NOT EXISTS lti_registrations (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    issuer            TEXT NOT NULL,
    client_id         TEXT NOT NULL,
    deployment_ids    TEXT NOT NULL DEFAULT '',
    auth_endpoint     TEXT NOT NULL DEFAULT '',
    jwks_uri          TEXT NOT NULL DEFAULT '',
    public_keyset     TEXT NOT NULL DEFAULT '',
    keyset_fetched_at DATETIME,
    directory_claim   TEXT NOT NULL DEFAULT '',
    enabled           INTEGER NOT NULL DEFAULT 1,
    created_at        DATETIME NOT NULL,
    updated_at        DATETIME NOT NULL,
    UNIQUE (issuer, client_id)
);

CREATE TABLE IF NOT EXISTS lti_tool_keys (
    k_id        TEXT PRIMARY KEY,
    private_key TEXT NOT NULL,
    public_jwk  TEXT NOT NULL,
    created_at  DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS lti_tickets (
    token_hash  TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL,
    context_id  TEXT NOT NULL DEFAULT '',
    roles       TEXT NOT NULL DEFAULT '',
    expires_at  DATETIME NOT NULL,
    consumed_at DATETIME,
    created_at  DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_lti_tickets_user_id ON lti_tickets (user_id);
CREATE INDEX IF NOT EXISTS idx_lti_tickets_expires_at ON lti_tickets (expires_at);
CREATE INDEX IF NOT EXISTS idx_lti_tickets_consumed_at ON lti_tickets (consumed_at);
