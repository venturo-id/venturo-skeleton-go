-- =====================================================
-- CORE SEEDER - SYSTEM ROLES
-- =====================================================
-- Seeder 001: Default system roles
--
-- Roles:
-- 1. super_admin  → Bypass all permission checks, no permissions needed
-- 2. administrator → Full access to every resource; assigned explicitly (signup grants no role)
--
-- Permission Levels: "viewer" (read), "editor" (CRUD), "admin" (CRUD + export/import/restore)
-- =====================================================

INSERT INTO core.roles (id, code, name, description, permissions, is_system, is_active) VALUES
-- Super Administrator: Full system access, bypasses all permission checks
(
    '00000000-0000-0000-0000-000000000001',
    'super_admin',
    'Super Administrator',
    'Full system access, bypasses all permission checks.',
    '{}'::jsonb,
    TRUE,
    TRUE
),
-- Administrator: All permissions at admin level
-- Cannot be modified by anyone except super_admin
(
    '00000000-0000-0000-0000-000000000002',
    'administrator',
    'Administrator',
    'Full access to every resource. Can manage users and custom roles.',
    '{
        "roles": "admin",
        "user-management": "admin",
        "finance.chart_of_accounts": "admin",
        "finance.journal_entries": "admin",
        "finance.a1_budget": "admin",
        "finance.a2_budget": "admin",
        "finance.a3_operating_fund_curia": "admin",
        "rental.customers": "admin",
        "rental.rentals": "admin",
        "rental.rental_items": "admin",
        "rental.item_categories": "admin",
        "rental.item_pricing": "admin",
        "rental.pricing_rules": "admin",
        "rental.rental_payments": "admin"
    }'::jsonb,
    TRUE,
    TRUE
)
ON CONFLICT DO NOTHING;
