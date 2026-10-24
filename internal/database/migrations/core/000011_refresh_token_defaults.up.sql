-- =====================================================
-- CORE SCHEMA - REFRESH TOKEN DEFAULTS
-- =====================================================
-- Migration 011:
--
-- 1. 000010 made refresh_tokens.session_id and signed_in_at NOT NULL
--    without a default. A release from before it inserts refresh tokens
--    without them, so while a rolling update or a rollback runs such a
--    release against this schema, every sign-in and refresh it serves
--    failed. With defaults it keeps working: each token it inserts starts
--    a session of its own.
--
--    Rule from here on (expand/contract): a migration keeps the previous
--    release working — a new NOT NULL column gets a DEFAULT, and a column
--    the previous release reads is not renamed or dropped in the same
--    release.
--
-- 2. The hourly purge deletes every expired refresh token, revoked or not;
--    the expires_at index only covered unrevoked ones, so it scanned the
--    table. No query filters on revoked_at alone.

ALTER TABLE core.refresh_tokens
    ALTER COLUMN session_id SET DEFAULT gen_random_uuid(),
    ALTER COLUMN signed_in_at SET DEFAULT now();

DROP INDEX IF EXISTS core.idx_refresh_tokens_expires_at;
CREATE INDEX idx_refresh_tokens_expires_at ON core.refresh_tokens(expires_at);

DROP INDEX IF EXISTS core.idx_refresh_tokens_revoked_at;
