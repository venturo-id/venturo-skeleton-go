-- =====================================================
-- CORE SCHEMA - TIMEBOX BASELINE
-- =====================================================
-- Migration 008: settle the schema the skeleton grew into before Timebox
-- features are built on it.
--
--   1. users.tokens_valid_after: the session epoch. Access tokens issued
--      before it are rejected (authz.Subject.TokensValidAfter).
--   2. NOT NULL on the flag, counter and audit columns the application
--      reads into non-pointer fields; one NULL there broke list endpoints.
--   3. Permission resources: "user-management" becomes "users", and
--      resources that do not exist in this application are dropped.
--   4. System roles (super_admin, administrator) are created here rather
--      than by a seeder, so every environment that migrates has them.
--      administrator holds every resource at admin level; a migration
--      that adds a resource grants it to administrator too.
--   5. API keys in the old "wiz_" format can no longer authenticate; they
--      are marked revoked so their owners see why.
--   6. Leftovers: an unused SQL function, and indexes duplicating a
--      unique index or primary key.
-- =====================================================

-- 1. Session epoch
ALTER TABLE core.users ADD COLUMN tokens_valid_after TIMESTAMPTZ;

COMMENT ON COLUMN core.users.tokens_valid_after IS
    'Access tokens issued before this instant are rejected. Moved forward on sign-out everywhere, password change, deactivation and refresh-token reuse. NULL = never revoked';

-- 2. NOT NULL where the application expects a value
UPDATE core.users SET is_active = TRUE WHERE is_active IS NULL;
UPDATE core.users SET is_email_verified = FALSE WHERE is_email_verified IS NULL;
UPDATE core.users SET failed_login_count = 0 WHERE failed_login_count IS NULL;
UPDATE core.users SET created_at = NOW() WHERE created_at IS NULL;
UPDATE core.users SET updated_at = created_at WHERE updated_at IS NULL;
ALTER TABLE core.users
    ALTER COLUMN is_active SET NOT NULL,
    ALTER COLUMN is_email_verified SET NOT NULL,
    ALTER COLUMN failed_login_count SET NOT NULL,
    ALTER COLUMN created_at SET NOT NULL,
    ALTER COLUMN updated_at SET NOT NULL;

UPDATE core.refresh_tokens SET device_info = '{}' WHERE device_info IS NULL;
UPDATE core.refresh_tokens SET created_at = NOW() WHERE created_at IS NULL;
ALTER TABLE core.refresh_tokens
    ALTER COLUMN device_info SET NOT NULL,
    ALTER COLUMN created_at SET NOT NULL;

UPDATE core.roles SET permissions = '{}' WHERE permissions IS NULL;
UPDATE core.roles SET is_system = FALSE WHERE is_system IS NULL;
UPDATE core.roles SET is_active = TRUE WHERE is_active IS NULL;
UPDATE core.roles SET created_at = NOW() WHERE created_at IS NULL;
UPDATE core.roles SET updated_at = created_at WHERE updated_at IS NULL;
ALTER TABLE core.roles
    ALTER COLUMN permissions SET NOT NULL,
    ALTER COLUMN is_system SET NOT NULL,
    ALTER COLUMN is_active SET NOT NULL,
    ALTER COLUMN created_at SET NOT NULL,
    ALTER COLUMN updated_at SET NOT NULL;

UPDATE core.user_roles SET created_at = NOW() WHERE created_at IS NULL;
ALTER TABLE core.user_roles ALTER COLUMN created_at SET NOT NULL;

UPDATE core.api_keys SET environment = 'live' WHERE environment IS NULL;
UPDATE core.api_keys SET scoped_permissions = '[]' WHERE scoped_permissions IS NULL;
UPDATE core.api_keys SET rate_limit = 1000 WHERE rate_limit IS NULL;
UPDATE core.api_keys SET rate_limit_window = 3600 WHERE rate_limit_window IS NULL;
UPDATE core.api_keys SET total_requests = 0 WHERE total_requests IS NULL;
UPDATE core.api_keys SET created_at = NOW() WHERE created_at IS NULL;
UPDATE core.api_keys SET updated_at = created_at WHERE updated_at IS NULL;
ALTER TABLE core.api_keys
    ALTER COLUMN environment SET NOT NULL,
    ALTER COLUMN scoped_permissions SET NOT NULL,
    ALTER COLUMN rate_limit SET NOT NULL,
    ALTER COLUMN rate_limit_window SET NOT NULL,
    ALTER COLUMN total_requests SET NOT NULL,
    ALTER COLUMN created_at SET NOT NULL,
    ALTER COLUMN updated_at SET NOT NULL;

-- 3. Permission resources
UPDATE core.roles
SET permissions = (permissions - 'user-management')
               || jsonb_build_object('users', permissions -> 'user-management')
WHERE permissions ? 'user-management'
  AND NOT permissions ? 'users';

UPDATE core.roles
SET permissions = (
        SELECT COALESCE(jsonb_object_agg(key, value), '{}'::jsonb)
        FROM jsonb_each(permissions)
        WHERE key IN ('users', 'roles')
    )
WHERE EXISTS (
    SELECT 1 FROM jsonb_object_keys(permissions) AS k(key)
    WHERE k.key NOT IN ('users', 'roles')
);

COMMENT ON COLUMN core.roles.permissions IS
    'JSONB object mapping registered resources (internal/shared/authz) to a level: viewer, editor or admin. Example: {"users": "editor"}';

-- 4. System roles
INSERT INTO core.roles (id, code, name, description, permissions, is_system, is_active) VALUES
(
    '00000000-0000-0000-0000-000000000001',
    'super_admin',
    'Super Administrator',
    'Full system access, bypasses all permission checks.',
    '{}'::jsonb,
    TRUE,
    TRUE
),
(
    '00000000-0000-0000-0000-000000000002',
    'administrator',
    'Administrator',
    'Full access to every resource. Can manage users and custom roles.',
    '{"users": "admin", "roles": "admin"}'::jsonb,
    TRUE,
    TRUE
)
ON CONFLICT (id) DO UPDATE SET
    code        = EXCLUDED.code,
    name        = EXCLUDED.name,
    description = EXCLUDED.description,
    permissions = EXCLUDED.permissions,
    is_system   = TRUE,
    is_active   = TRUE,
    deleted_at  = NULL,
    deleted_by  = NULL,
    updated_at  = NOW();

-- 5. Old-format API keys
UPDATE core.api_keys
SET revoked_at = NOW(),
    revoke_reason = 'API keys now start with tbx_; create a new key',
    updated_at = NOW()
WHERE revoked_at IS NULL
  AND key_prefix NOT LIKE 'tbx\_%';

COMMENT ON COLUMN core.api_keys.key_id IS
    'Short unique identifier, part of the full key format: tbx_<env>_<key_id>_<secret>';
COMMENT ON COLUMN core.api_keys.key_prefix IS
    'First characters of the key for display/identification (e.g. tbx_live_abc12345_a1b2...)';
COMMENT ON COLUMN core.api_keys.scoped_permissions IS
    'JSON array of the permissions the key may use (at least one); narrowed on every request to what the owner still holds';

-- 6. Leftovers
DROP FUNCTION IF EXISTS core.has_permission(JSONB, VARCHAR, VARCHAR);

-- Covered by users_email_active_key / users_username_active_key,
-- roles_code_active_key and the (user_id, role_id) primary key.
DROP INDEX IF EXISTS core.idx_users_email;
DROP INDEX IF EXISTS core.idx_users_username;
DROP INDEX IF EXISTS core.idx_roles_code;
DROP INDEX IF EXISTS core.idx_user_roles_user_id;
