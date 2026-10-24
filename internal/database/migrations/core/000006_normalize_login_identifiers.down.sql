-- =====================================================
-- CORE SCHEMA - NORMALIZED LOGIN IDENTIFIERS (rollback)
-- =====================================================
-- Drops the constraints only. Emails and usernames stay lowercased — the
-- original casing was not kept.
-- =====================================================

ALTER TABLE core.users
    DROP CONSTRAINT IF EXISTS users_username_normalized,
    DROP CONSTRAINT IF EXISTS users_email_normalized;
