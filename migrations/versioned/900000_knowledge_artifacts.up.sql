DO $$ BEGIN RAISE NOTICE '[Migration 900000] Creating knowledge_artifacts table...'; END $$;

CREATE TABLE IF NOT EXISTS knowledge_artifacts (
    id              VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id       INTEGER       NOT NULL,
    knowledge_id    VARCHAR(36)   NOT NULL,
    attempt         INTEGER       NOT NULL DEFAULT 1,
    artifact_type   VARCHAR(50)   NOT NULL,
    native_kind     VARCHAR(100),
    engine          VARCHAR(50)   NOT NULL,
    format          VARCHAR(50)   NOT NULL,
    size            BIGINT        NOT NULL DEFAULT 0,
    sha256          VARCHAR(64)   NOT NULL DEFAULT '',
    storage_key     TEXT          NOT NULL DEFAULT '',
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_knowledge_artifacts_knowledge_attempt
    ON knowledge_artifacts(knowledge_id, attempt);
CREATE INDEX IF NOT EXISTS idx_knowledge_artifacts_type
    ON knowledge_artifacts(knowledge_id, attempt, artifact_type);
CREATE INDEX IF NOT EXISTS idx_knowledge_artifacts_native_kind
    ON knowledge_artifacts(knowledge_id, attempt, native_kind);

-- One artifact per (knowledge, attempt, type, native_kind): at-least-once
-- task redelivery must replace the previous artifact instead of stacking
-- duplicate rows that double-charge quota.
CREATE UNIQUE INDEX IF NOT EXISTS uq_knowledge_artifacts_attempt
    ON knowledge_artifacts(tenant_id, knowledge_id, attempt, artifact_type, native_kind);

DO $$ BEGIN RAISE NOTICE '[Migration 900000] knowledge_artifacts table created'; END $$;

DO $$ BEGIN RAISE NOTICE '[Migration 900000] Adding current_attempt to knowledges...'; END $$;

ALTER TABLE knowledges
    ADD COLUMN IF NOT EXISTS current_attempt INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_knowledges_current_attempt
    ON knowledges(current_attempt);

DO $$ BEGIN RAISE NOTICE '[Migration 900000] current_attempt added to knowledges'; END $$;
