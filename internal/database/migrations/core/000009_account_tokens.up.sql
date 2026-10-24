-- =====================================================
-- CORE SCHEMA - ACCOUNT TOKENS
-- =====================================================
-- Migration 009: single-use tokens for the links emailed by the email
-- verification and password reset flows.
--
--   - Only the SHA-256 of a token is stored; the token itself exists in the
--     email alone.
--   - A token is bound to the address it was sent to: if the user's email
--     changes, tokens sent to the old address stop working.
--   - Using a token sets used_at in the same statement that checks it, so a
--     token works once even under concurrent requests. Issuing a new token
--     for the same purpose retires the user's earlier unused ones.
-- =====================================================

CREATE TABLE IF NOT EXISTS core.account_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,

    purpose VARCHAR(32) NOT NULL
        CONSTRAINT account_tokens_purpose_check CHECK (purpose IN ('verify_email', 'reset_password')),
    token_hash VARCHAR(64) NOT NULL,
    email VARCHAR(255) NOT NULL,

    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX account_tokens_token_hash_key ON core.account_tokens(token_hash);

-- Retiring a user's unused tokens when a new one is issued
CREATE INDEX idx_account_tokens_user_purpose
    ON core.account_tokens(user_id, purpose)
    WHERE used_at IS NULL;

-- Hourly purge of expired tokens
CREATE INDEX idx_account_tokens_expires_at ON core.account_tokens(expires_at);

COMMENT ON TABLE core.account_tokens IS
    'Single-use tokens behind emailed links: email verification and password reset';
COMMENT ON COLUMN core.account_tokens.token_hash IS
    'SHA-256 of the token; the token itself is only in the email';
COMMENT ON COLUMN core.account_tokens.email IS
    'Address the token was sent to; the token is void once the user''s email differs';
COMMENT ON COLUMN core.account_tokens.used_at IS
    'Set when the token is used or replaced by a newer one';
