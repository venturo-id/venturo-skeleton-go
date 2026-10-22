-- =====================================================
-- CORE SCHEMA - GOOGLE / EXTERNAL AUTH SUPPORT
-- =====================================================
-- Migration 005: Open up local-only assumptions in core.users so a user
-- can be created from an external identity provider (Firebase / Google
-- for now; Apple, Microsoft, etc. later) AND introduce a join table
-- that links one users row to one or many external identities.
--
-- Design notes:
--   - Local password stays on core.users.password_hash. password_hash is
--     made nullable because a Google-only user has no local password
--     until they explicitly set one via a future "set password" flow.
--   - username stays NOT NULL and unique. For Google sign-up, the
--     application layer derives a username from the email local-part
--     and resolves collisions with a numeric suffix (the schema does
--     not need to change for this).
--   - core.user_identities is the seam for multi-provider linking.
--     Lookup path on sign-in:
--        1. find by (provider, provider_user_id) → existing identity
--        2. else find users by email           → link to existing user
--        3. else create user + identity        → first-time sign-up
--   - One (provider, provider_user_id) pair maps to exactly one user
--     (enforced by UNIQUE). A single user may have multiple identities
--     (e.g. password + google linked to the same account).
-- =====================================================

-- 1. Make password_hash nullable so Google-only users can be inserted.
ALTER TABLE core.users
    ALTER COLUMN password_hash DROP NOT NULL;

COMMENT ON COLUMN core.users.password_hash IS
    'Bcrypt hash of the local password. NULL for users that registered through an external identity provider (Google via Firebase, etc.) and have not set a local password yet.';

-- 2. External identity links.
CREATE TABLE IF NOT EXISTS core.user_identities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,

    -- Provider code. Lowercase ASCII slug — 'google', 'apple', 'microsoft', ...
    provider VARCHAR(32) NOT NULL,

    -- Stable provider-side identifier. For Firebase-backed providers this
    -- is the Firebase UID (claim 'sub' / 'user_id' on the ID token).
    provider_user_id VARCHAR(255) NOT NULL,

    -- Email reported by the provider at the time of the link. May differ
    -- from core.users.email if the user later changes their primary email.
    email VARCHAR(255),

    -- Snapshot of provider claims captured at last sign-in: name, picture,
    -- locale, email_verified, firebase.sign_in_provider, etc. Useful for
    -- debugging "why was this user's avatar wrong" and future re-syncs.
    raw_profile JSONB NOT NULL DEFAULT '{}'::jsonb,

    last_login_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- One identity per (provider, provider_user_id) pair globally.
    CONSTRAINT user_identities_provider_user_unique
        UNIQUE (provider, provider_user_id)
);

-- Lookup by user (list a user's linked providers in profile UI).
CREATE INDEX idx_user_identities_user_id
    ON core.user_identities (user_id);

-- Optional lookup by email per-provider (helps if we ever need to
-- diagnose "user changed email at Google but we still have old one").
CREATE INDEX idx_user_identities_provider_email
    ON core.user_identities (provider, email);

COMMENT ON TABLE core.user_identities IS
    'External identity provider links. One user can have multiple identities (e.g. password + Google linked). One (provider, provider_user_id) pair belongs to exactly one user.';
COMMENT ON COLUMN core.user_identities.provider IS
    'Identity provider code: google, apple, microsoft, ... Local password lives on core.users.password_hash, not here.';
COMMENT ON COLUMN core.user_identities.provider_user_id IS
    'Stable provider-side identifier. Firebase uid for Firebase-backed providers.';
COMMENT ON COLUMN core.user_identities.raw_profile IS
    'Snapshot of provider claims captured at last sign-in. For Firebase: name, picture, locale, email_verified, firebase.sign_in_provider.';
