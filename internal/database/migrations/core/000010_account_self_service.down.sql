UPDATE core.roles SET permissions = permissions - 'audit_logs';

DELETE FROM core.account_tokens WHERE purpose = 'change_email';
ALTER TABLE core.account_tokens
    DROP CONSTRAINT account_tokens_new_email_check,
    DROP CONSTRAINT account_tokens_purpose_check,
    ADD CONSTRAINT account_tokens_purpose_check
        CHECK (purpose IN ('verify_email', 'reset_password'));
ALTER TABLE core.account_tokens DROP COLUMN new_email;

COMMENT ON TABLE core.account_tokens IS
    'Single-use tokens behind emailed links: email verification and password reset';
COMMENT ON COLUMN core.account_tokens.email IS
    'Address the token was sent to; the token is void once the user''s email differs';

DROP INDEX IF EXISTS core.idx_refresh_tokens_session_id;
ALTER TABLE core.refresh_tokens
    DROP COLUMN session_id,
    DROP COLUMN signed_in_at;

ALTER TABLE core.users DROP COLUMN timezone;
