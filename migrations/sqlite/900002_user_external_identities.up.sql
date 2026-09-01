CREATE TABLE IF NOT EXISTS user_external_identities (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      TEXT NOT NULL,
    authority    TEXT NOT NULL,
    external_uid TEXT NOT NULL,
    resolved_via TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL,
    last_seen_at DATETIME NOT NULL,
    UNIQUE (authority, external_uid)
);

CREATE INDEX IF NOT EXISTS idx_user_external_identities_user_id ON user_external_identities (user_id);
