# Tuai API Contract Documentation

**Last Updated:** 2026-10-21
**Total Modules:** 3 (Core, Finance, Rental)
**Total Endpoints Documented:** 126+

---

## 📚 Documentation Structure

```
docs/api-contract/
├── README.md (this file)
├── core/
│   ├── auth.md
│   ├── user.md
│   ├── role.md
│   ├── permission.md (planned)
│   └── permission-template.md (planned)
├── finance/
│   ├── chart-of-accounts.md
│   ├── journal-entries.md
│   ├── fiscal-periods.md
│   ├── contacts.md
│   ├── invoices.md (planned)
│   ├── payments.md (planned)
│   ├── expenses.md (planned)
│   ├── bank-accounts.md (planned)
│   ├── bank-reconciliation.md (planned)
│   ├── tax-configuration.md (planned)
│   └── financial-reports.md (planned)
└── rental/
    ├── customers.md
    ├── rentals.md
    ├── rental-payments.md
    ├── item-categories.md
    ├── rental-items.md
    ├── pricing-rules.md
    └── item-pricing.md
```

---

## 🎯 Core Module

Authentication, user management, and role-based access control.

**Base Path:** `/core/v1`

### ✅ Available Documentation

| File | Endpoints | Description | Status |
|------|-----------|-------------|--------|
| [auth.md](core/auth.md) | 7 | Sign up, sign in (password & Google), token refresh, logout, profile (`/me`) | ✅ Complete |
| [user.md](core/user.md) | 8 | User CRUD, role assignment, password management | ✅ Complete |
| [role.md](core/role.md) | 10 | Role CRUD, permission assignment (global roles) | ✅ Complete |

### 🚧 Planned Documentation

| File | Endpoints | Description | Status |
|------|-----------|-------------|--------|
| permission.md | 15 | Permission & Module management, CRUD templates | 🚧 Planned |
| permission-template.md | 6 | Permission template management | 🚧 Planned |

**Core Module Total:** 25 endpoints documented, 21 planned

---

## 💰 Finance Module

Complete double-entry accounting system with multi-currency support.

**Base Path:** `/finance/v1`

### ✅ Available Documentation

| File | Endpoints | Description | Status |
|------|-----------|-------------|--------|
| [chart-of-accounts.md](finance/chart-of-accounts.md) | 11 | Account types, categories, accounts CRUD, hierarchy | ✅ Complete |
| [journal-entries.md](finance/journal-entries.md) | 10 | Journal entry management, posting, voiding | ✅ Complete |
| [fiscal-periods.md](finance/fiscal-periods.md) | 12 | Fiscal year & period management, locking, closing | ✅ Complete |
| [contacts.md](finance/contacts.md) | 10 | Unified customer & vendor management | ✅ Complete |
| [a1-budget-projection.md](finance/a1-budget-projection.md) | 11 | A1 operating fund budget CRUD + approval workflow + PDF/Excel | ✅ Complete |
| [a2-budget-arcae.md](finance/a2-budget-arcae.md) | 11 | A2 per-ARCA budget (4 sub-funds) CRUD + approval + PDF/Excel | ✅ Complete |
| [a3-operating-fund-curia.md](finance/a3-operating-fund-curia.md) | 11 | A3 two-year comparison operating fund CRUD + approval + PDF/Excel | ✅ Complete |

### 🚧 Planned Documentation

| File | Endpoints | Description | Status |
|------|-----------|-------------|--------|
| invoices.md | 15 | Sales & purchase invoicing, PDF generation | 🚧 Planned |
| payments.md | 12 | Receipt & payment management, allocation | 🚧 Planned |
| expenses.md | 12 | Expense tracking with approval workflow | 🚧 Planned |
| bank-accounts.md | 8 | Bank account management | 🚧 Planned |
| bank-reconciliation.md | 15 | Bank reconciliation, transaction matching | 🚧 Planned |
| tax-configuration.md | 10 | Tax rates, compound taxes, Indonesia compliance | 🚧 Planned |
| financial-reports.md | 15 | Balance sheet, P&L, cash flow, aging reports | 🚧 Planned |

**Finance Module Total:** 76 endpoints documented, 87 planned

---

## 🏠 Rental Module

Rental management system for properties, vehicles, or equipment.

**Base Path:** `/rental/v1`

### ✅ Available Documentation

| File | Endpoints | Description | Status |
|------|-----------|-------------|--------|
| [customers.md](rental/customers.md) | 7 | Customer management, identity, blacklist | ✅ Complete |
| [rentals.md](rental/rentals.md) | 9 | Rental bookings, check-in/out, cancellation | ✅ Complete |
| [rental-payments.md](rental/rental-payments.md) | 7 | Payment tracking, verification, refunds | ✅ Complete |
| [item-categories.md](rental/item-categories.md) | 6 | Hierarchical category management for rental items | ✅ Complete |
| [rental-items.md](rental/rental-items.md) | 5 | Rental item/asset management with specs | ✅ Complete |
| [pricing-rules.md](rental/pricing-rules.md) | 5 | Pricing rule templates (daily, weekly, etc.) | ✅ Complete |
| [item-pricing.md](rental/item-pricing.md) | 7 | Item-specific pricing configuration | ✅ Complete |

**Rental Module Total:** 46 endpoints documented

---

## 📖 Documentation Standards

All API contract documentation follows a consistent format:

### Structure
- ✅ **Overview**: Module description and key features
- ✅ **Response Format**: Success and error response structures
- ✅ **Authentication & Authorization**: Required headers and permissions
- ✅ **Endpoints**: Detailed endpoint documentation
- ✅ **Best Practices**: Implementation guidelines
- ✅ **Changelog**: Version history

### Each Endpoint Includes
- ✅ HTTP Method and Path
- ✅ Authentication requirements
- ✅ Permission requirements
- ✅ Query/Path parameters
- ✅ Request body with validation rules
- ✅ Success response with examples
- ✅ Error responses with HTTP status codes
- ✅ Business rules and workflows

---

## 🔐 Authentication

All endpoints (except public auth endpoints) require JWT authentication:

```
Authorization: Bearer <access_token>
Content-Type: application/json
```

**Getting Access Token:**
1. Sign in: `POST /core/v1/auth/signin`
2. Include token in all subsequent requests
3. Refresh when expired: `POST /core/v1/auth/refresh`

---

## 🎭 Permission System

Permissions follow the format: `{module}.{resource}:{action}`

**Examples:**
- `core.users:read` - View users
- `core.roles:create` - Create roles
- `finance.journal_entries:post` - Post journal entries
- `finance.invoices:void` - Void invoices

---

## 📊 Response Format

### Success Response
```json
{
  "data": { /* response data */ },
  "message": "Success message",
  "meta": null,
  "errors": null
}
```

### Error Response
```json
{
  "data": null,
  "message": "Error message",
  "meta": null,
  "errors": {
    "field_name": ["Error detail message"]
  }
}
```

### Paginated Response
```json
{
  "data": [ /* array of items */ ],
  "message": "Success message",
  "meta": {
    "pagination": {
      "page": 1,
      "limit": 20,
      "total": 100,
      "total_pages": 5
    }
  }
}
```

**Note:** Pagination metadata is in `meta.pagination`, not inside `data`. Query parameter is `limit` (not `page_size`).

---

## 🔢 HTTP Status Codes

| Code | Description | When Used |
|------|-------------|-----------|
| 200 OK | Success | Successful GET, PUT, DELETE requests |
| 201 Created | Resource created | Successful POST requests |
| 400 Bad Request | Invalid request | Validation errors, invalid data |
| 401 Unauthorized | Not authenticated | Missing or invalid token |
| 403 Forbidden | No permission | User lacks required permission |
| 404 Not Found | Resource not found | Invalid ID or resource deleted |
| 409 Conflict | Resource conflict | Duplicate email, username, code |
| 429 Too Many Requests | Rate limited | Too many attempts |
| 500 Internal Server Error | Server error | Unexpected server errors |

---

## 🚀 Getting Started

### 1. Authentication
Start with the [auth.md](core/auth.md) documentation to:
- Register a new account
- Verify email
- Sign in and get access token

### 2. User & Role Management
Use [user.md](core/user.md) and [role.md](core/role.md) to:
- Create and manage users
- Set up roles and permissions
- Assign roles to users

### 3. Finance Setup
Follow finance documentation in order:
1. [chart-of-accounts.md](finance/chart-of-accounts.md) - Set up your COA
2. [fiscal-periods.md](finance/fiscal-periods.md) - Create fiscal years
3. [contacts.md](finance/contacts.md) - Add customers/vendors
4. [journal-entries.md](finance/journal-entries.md) - Record transactions

---

## 📝 Implementation Status

### ✅ Implemented Modules
- **Core Module**: Fully implemented in codebase
  - All endpoints are live and functional
  - See router: `internal/router/router.go`

### 🚧 Planned Modules
- **Finance Module**: Database schema ready, endpoints not implemented
  - Complete database migrations exist
  - Seeders available for testing
  - Application layer (handlers, services, DTOs) pending
  - Routes commented out in router

---

## 🛠️ For Developers

### Frontend Integration
1. Use TypeScript/JavaScript fetch or axios
2. Include JWT token in Authorization header
3. Handle error responses appropriately
4. Implement token refresh logic

### Example Request (JavaScript)
```javascript
const response = await fetch('/core/v1/users', {
  method: 'GET',
  headers: {
    'Authorization': `Bearer ${accessToken}`,
    'Content-Type': 'application/json'
  }
});

const data = await response.json();

if (!response.ok) {
  // Handle error
  console.error(data.message, data.errors);
} else {
  // Handle success
  console.log(data.data);
}
```

### Testing
- Use tools like Postman, Insomnia, or cURL
- Test with provided example requests
- Verify error handling

---

## 📞 Support

For questions or issues:
- **Email**: support@tuai.id
- **Documentation**: https://docs.tuai.id
- **Repository**: https://github.com/tuai/tuai-be
- **Issues**: https://github.com/tuai/tuai-be/issues

---

## 📋 Changelog

### Version 1.5 (2026-10-21)
- Removed Tenancy domain (clients, companies, company_users, branches, user_branches), Approval system, and translation overrides
- Removed endpoints: `POST /core/v1/auth/switch-company`, `GET /core/v1/auth/companies`, and all `/companies*`, `/branches*`, `/clients*`, `/admin/clients/*`, `/approval-configs*`, `/approval-requests*`, `/translation-overrides*` routes
- Deleted docs: `approval.md`, `branch.md`, `clients.md`, `company.md`, `translation-overrides.md`
- Updated auth contract: no `company`/`client` in responses, no company/client claims in JWT, `company_name` removed from signup, new users get no role
- Total: 126 endpoints documented

### Version 1.4 (2026-04-19)
- Added A3 Operating Fund Curia module (`finance.a3_operating_fund_curia`): 11 endpoints. Two-year comparison budget with 4 nested sub-blocks per year (operating income, operating expenses, other information, development office) stored as two JSONB columns + top-level FTE scalars
- Updated Approval system contract: added `a3_budget` feature key mapping to `finance.a3_operating_fund_curia`
- Total: 135 endpoints documented

### Version 1.3 (2026-04-18)
- Added A2 Budget ARCAE module (`finance.a2_budget`): 11 endpoints. Same pattern as A1 but with nested 4-ARCA matrix stored as JSONB; report output is 5-column landscape PDF / XLSX
- Updated Approval system contract: added `a2_budget` feature key mapping to `finance.a2_budget_arcae`
- Total: 124 endpoints documented

### Version 1.2 (2026-04-18)
- Added A1 Budget Projection module (`finance.a1_budget`): 11 endpoints including CRUD, submit/approve/reject via generic approval engine, and PDF/Excel export
- Updated Approval system contract: added `a1_budget` feature key + reff_type mapping, clarified consumer-status-vocabulary contract, added seeder example
- Total: 113 endpoints documented

### Version 1.1 (2026-03-22)
- Added Rental module documentation
- Item categories: 6 endpoints
- Rental items: 5 endpoints
- Pricing rules: 5 endpoints
- Item pricing: 7 endpoints
- Total: 102 endpoints documented

### Version 1.0 (2025-11-16)
- Initial API contract documentation
- Core module: auth, user, role (34 endpoints)
- Finance module: COA, journal entries, fiscal periods, contacts (43 endpoints)
- Total: 77 endpoints documented

---

## 🎯 Roadmap

### Short Term
- [ ] Complete Core module documentation (permission, permission-template)
- [ ] Complete Finance transactional modules (invoices, payments, expenses)

### Medium Term
- [ ] Complete Finance banking modules (bank accounts, reconciliation)
- [ ] Complete Finance configuration (tax configuration)
- [ ] Complete Finance reporting module

### Long Term
- [ ] Postman collection export
- [ ] Interactive API explorer
- [ ] Additional modules (CRM, HR, Inventory)

---

**Document Version:** 1.5
**Last Updated:** 2026-10-21
**Maintained By:** Tuai Development Team
