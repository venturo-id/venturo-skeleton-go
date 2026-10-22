-- =====================================================
-- CORE SCHEMA - API KEYS
-- =====================================================
-- Migration 004: API Keys for programmatic access
-- Owned per user, with optional scoped permissions
-- =====================================================

-- API Key environment type
CREATE TYPE core.api_key_environment AS ENUM ('live', 'test');

-- API Keys table
CREATE TABLE IF NOT EXISTS core.api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Ownership
    user_id UUID NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,

    -- Key identification
    key_id VARCHAR(16) NOT NULL,           -- Short identifier (e.g., abc12345)
    secret_hash VARCHAR(64) NOT NULL,      -- SHA256 hash of secret (never store plain)
    key_prefix VARCHAR(50) NOT NULL,       -- Display prefix (e.g., "wiz_live_abc12345_a1b2...")

    -- Metadata
    name VARCHAR(100) NOT NULL,            -- User-friendly name
    description TEXT,
    environment core.api_key_environment DEFAULT 'live',

    -- Security features
    scoped_permissions JSONB DEFAULT '[]', -- Subset of user's permissions (empty = inherit all)
    ip_whitelist TEXT[],                   -- Array of allowed IPs/CIDRs (NULL = allow all)

    -- Rate limiting
    rate_limit INT DEFAULT 1000,           -- Requests per window
    rate_limit_window INT DEFAULT 3600,    -- Window in seconds (default 1 hour)

    -- Lifecycle
    expires_at TIMESTAMPTZ,                -- Optional expiration (NULL = no expiry)
    revoked_at TIMESTAMPTZ,
    revoked_by UUID REFERENCES core.users(id),
    revoke_reason TEXT,

    -- Usage tracking
    last_used_at TIMESTAMPTZ,
    last_used_ip VARCHAR(45),
    total_requests BIGINT DEFAULT 0,

    -- Audit
    created_at TIMESTAMPTZ DEFAULT NOW(),
    created_by UUID,
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    updated_by UUID,
    deleted_at TIMESTAMPTZ,
    deleted_by UUID
);

-- Unique constraints (with soft delete filter)
CREATE UNIQUE INDEX api_keys_key_id_key ON core.api_keys(key_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX api_keys_secret_hash_key ON core.api_keys(secret_hash) WHERE deleted_at IS NULL;

-- Performance indexes
CREATE INDEX idx_api_keys_user_id ON core.api_keys(user_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_api_keys_environment ON core.api_keys(environment) WHERE deleted_at IS NULL;
CREATE INDEX idx_api_keys_expires_at ON core.api_keys(expires_at) WHERE deleted_at IS NULL AND expires_at IS NOT NULL;

-- GIN index for scoped permissions search
CREATE INDEX idx_api_keys_scoped_permissions ON core.api_keys USING GIN(scoped_permissions);

-- Comments
COMMENT ON TABLE core.api_keys IS 'API keys for programmatic access, owned by a user';
COMMENT ON COLUMN core.api_keys.key_id IS 'Short unique identifier, part of the full key format: wiz_<env>_<key_id>_<secret>';
COMMENT ON COLUMN core.api_keys.secret_hash IS 'SHA256 hash of the secret portion - never store plain text';
COMMENT ON COLUMN core.api_keys.key_prefix IS 'First N characters of key for display/identification (e.g., wiz_live_abc12345_a1b2...)';
COMMENT ON COLUMN core.api_keys.scoped_permissions IS 'JSON array of allowed permissions (empty = inherit all from user)';
COMMENT ON COLUMN core.api_keys.ip_whitelist IS 'Array of allowed IP addresses or CIDR ranges (NULL = allow all)';
COMMENT ON COLUMN core.api_keys.rate_limit IS 'Maximum requests allowed per rate_limit_window';
COMMENT ON COLUMN core.api_keys.rate_limit_window IS 'Rate limit window in seconds (default 3600 = 1 hour)';
