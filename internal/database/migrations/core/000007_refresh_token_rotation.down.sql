-- =====================================================
-- CORE SCHEMA - REFRESH TOKEN ROTATION CHAIN (rollback)
-- =====================================================

DROP INDEX IF EXISTS core.idx_refresh_tokens_rotated_from_id;

ALTER TABLE core.refresh_tokens
    DROP COLUMN IF EXISTS rotated_from_id;
