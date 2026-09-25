-- Per-registration handoff override for launches: an empty value falls back to
-- the global LTI_HANDOFF_URL, so one tool instance can serve several placements
-- (e.g. course navigation to an external web app, user navigation to the
-- built-in browser handoff).
ALTER TABLE lti_registrations ADD COLUMN IF NOT EXISTS handoff_url TEXT NOT NULL DEFAULT '';
