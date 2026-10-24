-- =====================================================
-- CORE SCHEMA - EMAILED CODES, GOOGLE OAUTH
-- =====================================================
-- Migration 012:
--
-- 1. Email verification, password reset and email change send a 6-digit
--    code instead of a link. A code lives in core.account_tokens like a
--    link token did, with two differences:
--      - token_hash is an HMAC-SHA256 keyed with a server secret over the
--        row id and the code: six digits are too few for a plain hash, and
--        binding the row id keeps equal codes of two rows from colliding
--        on the unique index.
--      - attempts counts wrong guesses; at OTP_MAX_ATTEMPTS the code is
--        dead. A code is looked up by (user, purpose), never by its hash.
--    A fourth purpose, reset_grant: the short-lived token a correct
--    password reset code is traded for (a random token, SHA-256 hashed,
--    like a link token).
--
-- 2. Google sign-in no longer goes through Firebase: the backend is the
--    OAuth client, and an identity is keyed by Google's own account id
--    ("sub") instead of the Firebase uid. Identities linked through
--    Firebase are moved to the Google id their stored claims carry
--    (firebase.identities["google.com"][0]), so they keep signing in.
--
-- Expand/contract: the previous release keeps working against this
-- schema — attempts has a default, and it never reads reset_grant rows.
-- =====================================================

-- 1. Codes
ALTER TABLE core.account_tokens
    ADD COLUMN attempts SMALLINT NOT NULL DEFAULT 0;

ALTER TABLE core.account_tokens
    DROP CONSTRAINT account_tokens_purpose_check,
    ADD CONSTRAINT account_tokens_purpose_check
        CHECK (purpose IN ('verify_email', 'reset_password', 'change_email', 'reset_grant'));

COMMENT ON TABLE core.account_tokens IS
    'Single-use secrets sent by email or traded for one: verification, password reset and email change codes, and password reset grants';
COMMENT ON COLUMN core.account_tokens.token_hash IS
    'Codes: HMAC-SHA256 (server secret) over the row id and the code. reset_grant: SHA-256 of the token. The secret itself is never stored';
COMMENT ON COLUMN core.account_tokens.attempts IS
    'Wrong guesses at a code; the code is dead once it reaches OTP_MAX_ATTEMPTS';

-- 2. Google identities: Firebase uid -> Google account id
UPDATE core.user_identities
SET provider_user_id = raw_profile #>> '{firebase,identities,google.com,0}',
    updated_at = NOW()
WHERE provider = 'google'
  AND raw_profile #>> '{firebase,identities,google.com,0}' IS NOT NULL
  AND raw_profile #>> '{firebase,identities,google.com,0}' <> provider_user_id;

COMMENT ON COLUMN core.user_identities.provider_user_id IS
    'Stable provider-side identifier. Google: the account id ("sub" of its ID token)';
COMMENT ON COLUMN core.user_identities.raw_profile IS
    'Snapshot of the provider''s ID token claims at the last sign-in (name, picture, email_verified, ...)';
