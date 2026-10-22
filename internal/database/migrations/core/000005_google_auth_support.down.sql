-- =====================================================
-- CORE SCHEMA - GOOGLE / EXTERNAL AUTH SUPPORT (rollback)
-- =====================================================
-- WARNING: This rollback will FAIL if there are existing rows in
-- core.users with NULL password_hash (Google-only users). Delete or
-- backfill them before rolling back.
-- =====================================================

DROP INDEX IF EXISTS core.idx_user_identities_provider_email;
DROP INDEX IF EXISTS core.idx_user_identities_user_id;

DROP TABLE IF EXISTS core.user_identities;

ALTER TABLE core.users
    ALTER COLUMN password_hash SET NOT NULL;
