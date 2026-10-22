# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Tuai Backend is a single-tenant business management platform built with Go 1.25, Gin web framework, and PostgreSQL. It features a modular monolithic architecture with role-based access control (RBAC). There is no client/company/branch tenancy layer — access is governed only by global roles.

## Development Commands

### Setup & Development

```bash
cp .env.example .env           # Configure environment variables
make db-setup                  # Run migrations and seeders
make dev                       # Start with hot reload

# Alternative modes
make run                       # Run without hot reload
make build                     # Build production binary (outputs to bin/)
```

### Database Operations

```bash
# Migrations — core module (app data), tracked in schema_migrations_core
make migrate-up                # Run all pending migrations
make migrate-down MODULE=core  # Rollback last migration in a module
make migrate-version           # Show current version
make migrate-create NAME=create_foo MODULE=core   # Create new core migration

# Force migration version (if stuck)
make migrate-force V=1 MODULE=core

# Seeding
make seed                      # Run all seeders
make seed-core                 # Run core module seeders only

# Complete reset
make db-reset                  # Drop database, recreate, migrate, and seed
```

### Testing

```bash
go test ./internal/modules/core/auth/...  # Specific package
go test ./...                              # All tests
go test -cover ./...                       # With coverage
```

## Architecture & Patterns

### Module Structure

Every feature module follows this pattern:

```
module_name/
├── domain/              # Business entities
├── dto/                 # Request/Response DTOs with validation tags
├── handler/             # HTTP handlers (Gin handlers)
├── repository/          # Data access layer
├── service/             # Business logic
└── main.{module}.go     # Module initialization with Initialize() and SetupRoutes()
```

Each `main.{module}.go` exports:

- `Initialize(db *pgxpool.Pool, ...) *Module` - Dependency injection
- `SetupRoutes(router *gin.RouterGroup)` - Route registration

### Versioned API Routes

Routes are organized by domain and version:

- **Core**: `/core/v1/...` (auth, users, roles, API keys)

All modules are initialized in [internal/router/router.go](internal/router/router.go).

### Access Model

1. **Middleware Chain**: `JWTAuth()` → `RequirePermission()`
2. **Roles are global**: assigned in `core.user_roles`; a user's effective permissions are the merged permissions of all their active roles
3. **Permission cache**: effective permissions live in Redis per user (`perms:user:<user_id>`, see [internal/shared/authz/](internal/shared/authz/)); invalidate it whenever a role or a role assignment changes
4. **New users start with no role**: signup (email or Google) grants zero permissions until a super_admin assigns a role

### RBAC Middleware

Located in [internal/middleware/](internal/middleware/):

```go
// Role-based
middleware.RequireRole("admin", "manager")       // Has any role
middleware.RequireAllRoles("admin", "manager")   // Has all roles

// Permission-based
middleware.RequirePermission("users:create")           // Has specific permission
middleware.RequireAnyPermission("users:create", "users:update")  // Has any
middleware.RequireAllPermissions("users:read", "users:write")    // Has all
```

### Authentication Flow

1. **Sign Up**: `POST /core/v1/auth/signup`
2. **Sign In**: `POST /core/v1/auth/signin` → Returns access + refresh tokens
3. **Refresh Token**: `POST /core/v1/auth/refresh` → New access token
4. **Logout**: `POST /core/v1/auth/logout` or `POST /core/v1/auth/logout-all`

JWT claims include: `user_id`, `email`, `username`, `full_name`, `is_super_admin`, `roles` (permissions are looked up from Redis, not embedded)

## Adding New Features

### Creating a New Module

1. Create module directory: `internal/modules/{domain}/{module_name}/`
2. Create subdirectories: `domain/`, `dto/`, `handler/`, `repository/`, `service/`
3. Create `main.{module_name}.go` with `Initialize()` and `SetupRoutes()`
4. Register in [internal/router/router.go](internal/router/router.go)

### Creating Database Migrations

```bash
make migrate-create NAME=create_customers_table MODULE=core
```

Migration conventions:

- Add indexes on foreign keys
- Use `TIMESTAMP WITH TIME ZONE` for timestamps (stored in UTC)
- Use `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`

## Important Conventions

### Error Handling

Use helpers from [internal/shared/response/](internal/shared/response/):

```go
response.Success(c, http.StatusOK, "Success", data)
response.Error(c, http.StatusBadRequest, "Validation failed", errors)
response.Paginated(c, data, totalCount, page, limit)
```

### Logging

Use structured logging from [pkg/logger/](pkg/logger/):

```go
logger.Info("User created", logger.String("user_id", userID))
logger.Error("Failed to create user", logger.Err(err))
```

### Validation

Use `binding` tags with Gin's validation:

```go
type CreateUserRequest struct {
    Name   string `json:"name" binding:"required,min=2,max=100"`
    Email  string `json:"email" binding:"required,email"`
}
```

Custom validators in [pkg/validator/](pkg/validator/).

## Common Gotchas

### Migration Issues

- Check current version with `make migrate-version`
- Use `make migrate-force V=X MODULE=core` to force version if stuck
- Core migrations live under `internal/database/migrations/core` and are tracked in `schema_migrations_core`

### Access Control

- Every non-public list/read endpoint needs its own `RequirePermission(...)` — there is no tenant scope to hide data
- Records owned by one user (e.g. API keys) must be filtered by `user_id` in the repository
- Changing a role's permissions or a user's roles must invalidate the authz cache (`InvalidateAll` / `InvalidateUser`)

### Hot Reload (Air)

- Configured in `.air.toml`
- Build errors logged to `build-errors.log`

## Testing Endpoints

```bash
# Health check
curl http://localhost:8080/health

# Sign in
curl -X POST http://localhost:8080/core/v1/auth/signin \
  -H "Content-Type: application/json" \
  -d '{"login": "admin@tuai.com", "password": "admin123"}'

# Authenticated request
curl http://localhost:8080/core/v1/users \
  -H "Authorization: Bearer YOUR_ACCESS_TOKEN"
```
