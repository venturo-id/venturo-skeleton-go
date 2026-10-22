-- =====================================================
-- CORE SCHEMA - API KEYS (ROLLBACK)
-- =====================================================
-- Migration 004 Down: Remove API Keys table and type
-- =====================================================

-- Drop indexes first (they depend on table)
DROP INDEX IF EXISTS core.idx_api_keys_scoped_permissions;
DROP INDEX IF EXISTS core.idx_api_keys_expires_at;
DROP INDEX IF EXISTS core.idx_api_keys_environment;
DROP INDEX IF EXISTS core.idx_api_keys_user_id;
DROP INDEX IF EXISTS core.api_keys_secret_hash_key;
DROP INDEX IF EXISTS core.api_keys_key_id_key;

-- Drop table
DROP TABLE IF EXISTS core.api_keys;

-- Drop enum type
DROP TYPE IF EXISTS core.api_key_environment;
