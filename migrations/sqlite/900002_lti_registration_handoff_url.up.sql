-- Mirrors versioned migration 900002_lti_registration_handoff_url: per-
-- registration handoff override, empty falls back to the global
-- LTI_HANDOFF_URL.
ALTER TABLE lti_registrations ADD COLUMN handoff_url TEXT NOT NULL DEFAULT '';
