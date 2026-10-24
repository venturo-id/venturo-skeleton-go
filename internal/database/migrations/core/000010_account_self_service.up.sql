-- =====================================================
-- CORE SCHEMA - ACCOUNT SELF-SERVICE
-- =====================================================
-- Migration 010: what a user manages about their own account.
--
--   1. users.timezone: the IANA zone a user's dates are shown and grouped
--      in. Timestamps stay UTC in the database; the zone only says how to
--      read them. Validated by the application (time.LoadLocation).
--   2. refresh_tokens.session_id / signed_in_at: a session is a chain of
--      rotated refresh tokens. Rotation carries both columns over, and
--      access tokens carry the session id as the "sid" claim, so a user can
--      list their sessions and end one — its access tokens included.
--   3. account_tokens: a third purpose, change_email. The link goes to the
--      new address (new_email); the token stays bound to the account's
--      current address (email), like the others.
--   4. Permission resource audit_logs, granted to administrator. The audit
--      log itself is stored in MongoDB, not here.
-- =====================================================

-- 1. Timezone
ALTER TABLE core.users
    ADD COLUMN timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Jakarta';

COMMENT ON COLUMN core.users.timezone IS
    'IANA time zone (e.g. Asia/Jakarta) the user''s dates are shown and grouped in; timestamps stay UTC';

-- 2. Sessions. A token issued before this migration starts a session of
-- its own; its access tokens carry no session id and are not checked.
ALTER TABLE core.refresh_tokens
    ADD COLUMN session_id UUID,
    ADD COLUMN signed_in_at TIMESTAMPTZ;

UPDATE core.refresh_tokens SET session_id = id, signed_in_at = created_at;

ALTER TABLE core.refresh_tokens
    ALTER COLUMN session_id SET NOT NULL,
    ALTER COLUMN signed_in_at SET NOT NULL;

CREATE INDEX idx_refresh_tokens_session_id ON core.refresh_tokens(session_id);

COMMENT ON COLUMN core.refresh_tokens.session_id IS
    'Session the token belongs to: kept on rotation, and carried by access tokens as the "sid" claim';
COMMENT ON COLUMN core.refresh_tokens.signed_in_at IS
    'When the session started (its sign-in), kept on rotation';

-- 3. Email change links
ALTER TABLE core.account_tokens ADD COLUMN new_email VARCHAR(255);

ALTER TABLE core.account_tokens
    DROP CONSTRAINT account_tokens_purpose_check,
    ADD CONSTRAINT account_tokens_purpose_check
        CHECK (purpose IN ('verify_email', 'reset_password', 'change_email')),
    ADD CONSTRAINT account_tokens_new_email_check
        CHECK ((purpose = 'change_email') = (new_email IS NOT NULL));

COMMENT ON TABLE core.account_tokens IS
    'Single-use tokens behind emailed links: email verification, password reset and email change';
COMMENT ON COLUMN core.account_tokens.email IS
    'The account''s address when the token was issued; the token is void once the user''s email differs';
COMMENT ON COLUMN core.account_tokens.new_email IS
    'change_email only: the address the link was sent to, which the account moves to';

-- 4. Audit log permission
UPDATE core.roles
SET permissions = permissions || '{"audit_logs": "admin"}'
WHERE code = 'administrator';
