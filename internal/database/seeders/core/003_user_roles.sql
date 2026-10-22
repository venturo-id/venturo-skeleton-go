-- =====================================================
-- CORE SEEDER - USER ROLES
-- =====================================================
-- Seeder 003: Assign roles to the seeded users
-- =====================================================

INSERT INTO core.user_roles (user_id, role_id) VALUES
-- Owner - super_admin role
(
    '10000000-0000-0000-0000-000000000001',
    '00000000-0000-0000-0000-000000000001'
),
-- Client - administrator role
(
    '10000000-0000-0000-0000-000000000002',
    '00000000-0000-0000-0000-000000000002'
)
ON CONFLICT DO NOTHING;
