DO $$ BEGIN RAISE NOTICE '[Migration 900000 down] Dropping knowledge_artifacts...'; END $$;

DROP INDEX IF EXISTS uq_knowledge_artifacts_attempt;
DROP TABLE IF EXISTS knowledge_artifacts;

DO $$ BEGIN RAISE NOTICE '[Migration 900000 down] Removing current_attempt from knowledges...'; END $$;

ALTER TABLE knowledges
    DROP COLUMN IF EXISTS current_attempt;

DO $$ BEGIN RAISE NOTICE '[Migration 900000 down] Reverted'; END $$;
