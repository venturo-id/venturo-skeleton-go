-- Rollback: 000012_email_codes_google_oauth
--
-- Identities linked through Firebase go back to their Firebase uid (the
-- "user_id" claim stored with them). Identities linked since, straight
-- from Google, have no Firebase uid and keep the Google id: Firebase
-- sign-in finds them by email instead.

UPDATE core.user_identities
SET provider_user_id = raw_profile ->> 'user_id',
    updated_at = NOW()
WHERE provider = 'google'
  AND raw_profile ? 'firebase'
  AND raw_profile ->> 'user_id' IS NOT NULL;

COMMENT ON COLUMN core.user_identities.provider_user_id IS
    'Stable provider-side identifier. Firebase uid for Firebase-backed providers.';
COMMENT ON COLUMN core.user_identities.raw_profile IS
    'Snapshot of provider claims captured at last sign-in. For Firebase: name, picture, locale, email_verified, firebase.sign_in_provider.';

-- Codes cannot be redeemed as links: drop every pending one.
DELETE FROM core.account_tokens WHERE purpose = 'reset_grant' OR used_at IS NULL;

ALTER TABLE core.account_tokens
    DROP CONSTRAINT account_tokens_purpose_check,
    ADD CONSTRAINT account_tokens_purpose_check
        CHECK (purpose IN ('verify_email', 'reset_password', 'change_email'));

ALTER TABLE core.account_tokens DROP COLUMN attempts;

COMMENT ON TABLE core.account_tokens IS
    'Single-use tokens behind emailed links: email verification, password reset and email change';
COMMENT ON COLUMN core.account_tokens.token_hash IS
    'SHA-256 of the token; the token itself is only in the email';
