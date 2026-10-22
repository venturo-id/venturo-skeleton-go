-- =====================================================
-- CORE2 SCHEMA - RBAC (SIMPLIFIED)
-- =====================================================
-- Migration 003: Roles with JSON-based Permissions
-- =====================================================

-- Roles table with JSONB permissions
CREATE TABLE IF NOT EXISTS core.roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Identity
    code VARCHAR(50) NOT NULL,  -- e.g., 'super_admin', 'admin', 'member'
    name VARCHAR(100) NOT NULL, -- e.g., 'Super Administrator'
    description TEXT,

    -- Permissions as JSONB (level-based)
    -- Format: {"resource": "level"}
    -- Levels: "viewer", "editor", "admin" (mapped to actions in backend config)
    -- Example: {"dashboard": "viewer", "user-management": "admin", "jurnal-umum": "editor"}
    permissions JSONB DEFAULT '{}',

    -- Scope
    is_system BOOLEAN DEFAULT FALSE,  -- System roles cannot be modified

    -- Status
    is_active BOOLEAN DEFAULT TRUE,

    -- Audit
    created_at TIMESTAMPTZ DEFAULT NOW(),
    created_by UUID,
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    updated_by UUID,
    deleted_at TIMESTAMPTZ,
    deleted_by UUID
);

-- Unique constraint: code must be unique among active roles
CREATE UNIQUE INDEX roles_code_active_key ON core.roles(code)
    WHERE deleted_at IS NULL;

-- Performance indexes
CREATE INDEX idx_roles_code ON core.roles(code) WHERE deleted_at IS NULL;
CREATE INDEX idx_roles_is_system ON core.roles(is_system) WHERE deleted_at IS NULL;
CREATE INDEX idx_roles_is_active ON core.roles(is_active) WHERE deleted_at IS NULL;
CREATE INDEX idx_roles_permissions ON core.roles USING GIN(permissions);


-- User roles junction table
CREATE TABLE IF NOT EXISTS core.user_roles (
    user_id UUID NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
    role_id UUID NOT NULL REFERENCES core.roles(id) ON DELETE CASCADE,

    -- Audit
    created_at TIMESTAMPTZ DEFAULT NOW(),
    created_by UUID,

    PRIMARY KEY (user_id, role_id)
);

-- Performance indexes
CREATE INDEX idx_user_roles_user_id ON core.user_roles(user_id);
CREATE INDEX idx_user_roles_role_id ON core.user_roles(role_id);


-- =====================================================
-- COMMENTS
-- =====================================================
COMMENT ON TABLE core.roles IS 'Role definitions with JSON-based permissions';
COMMENT ON COLUMN core.roles.code IS 'Unique identifier for the role (snake_case)';
COMMENT ON COLUMN core.roles.permissions IS 'JSONB object mapping resources to permission levels (viewer/editor/admin)';
COMMENT ON COLUMN core.roles.is_system IS 'System roles cannot be modified or deleted';

COMMENT ON TABLE core.user_roles IS 'Junction table linking users to roles';


-- =====================================================
-- HELPER FUNCTIONS
-- =====================================================

-- Function to check if a role has a specific resource permission
CREATE OR REPLACE FUNCTION core.has_permission(
    p_permissions JSONB,
    p_resource VARCHAR,
    p_level VARCHAR DEFAULT NULL
) RETURNS BOOLEAN AS $$
BEGIN
    IF p_level IS NULL THEN
        -- Check if resource exists (any level)
        RETURN p_permissions ? p_resource;
    ELSE
        -- Check if resource has specific level
        RETURN p_permissions->>p_resource = p_level;
    END IF;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

COMMENT ON FUNCTION core.has_permission IS 'Check if permissions JSONB contains a resource with optional level check';

-- Example usage:
-- SELECT core.has_permission(permissions, 'dashboard') FROM core.roles WHERE code = 'admin';
-- SELECT core.has_permission(permissions, 'dashboard', 'viewer') FROM core.roles WHERE code = 'admin';
