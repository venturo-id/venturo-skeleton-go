-- Reverses the schema changes of 000008. Data changes stay: system roles
-- keep their current permissions, and revoked API keys stay revoked.

CREATE INDEX IF NOT EXISTS idx_users_email ON core.users(email);
CREATE INDEX IF NOT EXISTS idx_users_username ON core.users(username);
CREATE INDEX IF NOT EXISTS idx_roles_code ON core.roles(code) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_user_roles_user_id ON core.user_roles(user_id);

CREATE OR REPLACE FUNCTION core.has_permission(
    p_permissions JSONB,
    p_resource VARCHAR,
    p_level VARCHAR DEFAULT NULL
) RETURNS BOOLEAN AS $$
BEGIN
    IF p_level IS NULL THEN
        RETURN p_permissions ? p_resource;
    ELSE
        RETURN p_permissions->>p_resource = p_level;
    END IF;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

UPDATE core.roles
SET permissions = (permissions - 'users')
               || jsonb_build_object('user-management', permissions -> 'users')
WHERE permissions ? 'users';

ALTER TABLE core.api_keys
    ALTER COLUMN environment DROP NOT NULL,
    ALTER COLUMN scoped_permissions DROP NOT NULL,
    ALTER COLUMN rate_limit DROP NOT NULL,
    ALTER COLUMN rate_limit_window DROP NOT NULL,
    ALTER COLUMN total_requests DROP NOT NULL,
    ALTER COLUMN created_at DROP NOT NULL,
    ALTER COLUMN updated_at DROP NOT NULL;

ALTER TABLE core.user_roles ALTER COLUMN created_at DROP NOT NULL;

ALTER TABLE core.roles
    ALTER COLUMN permissions DROP NOT NULL,
    ALTER COLUMN is_system DROP NOT NULL,
    ALTER COLUMN is_active DROP NOT NULL,
    ALTER COLUMN created_at DROP NOT NULL,
    ALTER COLUMN updated_at DROP NOT NULL;

ALTER TABLE core.refresh_tokens
    ALTER COLUMN device_info DROP NOT NULL,
    ALTER COLUMN created_at DROP NOT NULL;

ALTER TABLE core.users
    ALTER COLUMN is_active DROP NOT NULL,
    ALTER COLUMN is_email_verified DROP NOT NULL,
    ALTER COLUMN failed_login_count DROP NOT NULL,
    ALTER COLUMN created_at DROP NOT NULL,
    ALTER COLUMN updated_at DROP NOT NULL;

ALTER TABLE core.users DROP COLUMN IF EXISTS tokens_valid_after;
