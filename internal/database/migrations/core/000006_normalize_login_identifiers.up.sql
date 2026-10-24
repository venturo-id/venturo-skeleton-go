-- =====================================================
-- CORE SCHEMA - NORMALIZED LOGIN IDENTIFIERS
-- =====================================================
-- Migration 006: Store email and username lowercased and trimmed, and let
-- the database refuse anything else.
--
-- Before this, both were compared exactly: "User@x.com" and "user@x.com"
-- could be two accounts, and Google sign-in (which lowercases) could miss
-- an existing mixed-case account and create a duplicate. With every row
-- normalized, the existing partial unique indexes become case-insensitive.
--
-- The application normalizes on input (signup, admin create/update,
-- sign-in lookup, seed-admin); the CHECK constraints catch any path that
-- forgets.
-- =====================================================

-- 1. Live accounts that differ only by case or surrounding spaces cannot
--    be merged automatically — pick one by hand (soft-delete or rename the
--    other). The failed run rolls back but leaves version 6 marked dirty, so
--    reset it before running again:
--      make migrate-force V=5 MODULE=core && make migrate-up
DO $$
DECLARE
    clashes TEXT;
BEGIN
    SELECT string_agg(identifier, ', ') INTO clashes
    FROM (
        SELECT 'email ' || lower(btrim(email)) AS identifier
        FROM core.users
        WHERE deleted_at IS NULL
        GROUP BY lower(btrim(email))
        HAVING count(*) > 1

        UNION ALL

        SELECT 'username ' || lower(btrim(username))
        FROM core.users
        WHERE deleted_at IS NULL
        GROUP BY lower(btrim(username))
        HAVING count(*) > 1
    ) duplicates;

    IF clashes IS NOT NULL THEN
        RAISE EXCEPTION 'Live users collide once lowercased: %. Soft-delete or rename one of each pair, then run: make migrate-force V=5 MODULE=core && make migrate-up', clashes;
    END IF;
END $$;

-- 2. Normalize every row, soft-deleted ones included, so the CHECK holds.
UPDATE core.users
SET email = lower(btrim(email))
WHERE email <> lower(btrim(email));

UPDATE core.users
SET username = lower(btrim(username))
WHERE username <> lower(btrim(username));

-- 3. Refuse un-normalized values from now on.
ALTER TABLE core.users
    ADD CONSTRAINT users_email_normalized CHECK (email = lower(btrim(email))),
    ADD CONSTRAINT users_username_normalized CHECK (username = lower(btrim(username)));

COMMENT ON CONSTRAINT users_email_normalized ON core.users IS
    'Emails are stored lowercased and trimmed, which makes the unique index case-insensitive';
COMMENT ON CONSTRAINT users_username_normalized ON core.users IS
    'Usernames are stored lowercased and trimmed, which makes the unique index case-insensitive';
