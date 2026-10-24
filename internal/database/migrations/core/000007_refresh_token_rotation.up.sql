-- =====================================================
-- CORE SCHEMA - REFRESH TOKEN ROTATION CHAIN
-- =====================================================
-- Migration 007: Record which refresh token each rotated token replaced.
--
-- POST /auth/refresh revokes the presented token and issues a new one.
-- Without a link between the two, a revoked token looked the same whether
-- it was logged out or rotated. With rotated_from_id the service can tell
-- when an already-rotated token is presented again — a sign the token was
-- copied — and revoke every session of that user.
-- =====================================================

ALTER TABLE core.refresh_tokens
    ADD COLUMN rotated_from_id UUID REFERENCES core.refresh_tokens(id) ON DELETE SET NULL;

CREATE INDEX idx_refresh_tokens_rotated_from_id
    ON core.refresh_tokens(rotated_from_id)
    WHERE rotated_from_id IS NOT NULL;

COMMENT ON COLUMN core.refresh_tokens.rotated_from_id IS
    'The token this one replaced on /auth/refresh. NULL for tokens issued at sign-in';
