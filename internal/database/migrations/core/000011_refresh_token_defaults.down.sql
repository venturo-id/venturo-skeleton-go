-- Rollback: 000011_refresh_token_defaults

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_revoked_at
    ON core.refresh_tokens(revoked_at) WHERE revoked_at IS NOT NULL;

DROP INDEX IF EXISTS core.idx_refresh_tokens_expires_at;
CREATE INDEX idx_refresh_tokens_expires_at
    ON core.refresh_tokens(expires_at) WHERE revoked_at IS NULL;

ALTER TABLE core.refresh_tokens
    ALTER COLUMN session_id DROP DEFAULT,
    ALTER COLUMN signed_in_at DROP DEFAULT;
