# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Timebox Backend is the auth + RBAC foundation of the Timebox product: a single-tenant API built with Go 1.26, Gin, PostgreSQL and Redis, as a modular monolith with role-based access control (RBAC). There is no client/company/branch tenancy layer — access is governed only by global roles. Redis is required (authz cache and rate limits): the API does not start without it. MongoDB is optional and holds only the audit log (`MONGO_URI`; without it nothing is recorded).

## Development Commands

### Setup & Development

```bash
cp .env.example .env           # Configure env vars — JWT_SECRET and SEED_ADMIN_* are required; DB_*/REDIS_* must reach running servers
make db-setup                  # Run migrations, then create the super admin
make dev                       # Start with hot reload (air)

# Alternative modes
make run                       # Run without hot reload
make build                     # Build production binary (bin/timebox-api)
make fmt                       # gofmt -w .
make lint                      # gofmt check + go vet + staticcheck (pinned, via go run)
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
make seed-admin                # Create the super admin from SEED_ADMIN_* (idempotent); `make seed` is an alias

# Complete reset (refused when ENV=production)
make db-reset                  # Drop database, recreate, migrate, and seed
```

There are no SQL seeders: the system roles `super_admin` and `administrator` are created by migration `000008`.

### Testing

```bash
make test                                 # go test -race ./...
make test-integration                     # TestIntegration_* against TEST_DATABASE_URL (a migrated test DB) and, optionally, TEST_MONGO_URI
go test ./internal/modules/core/auth/...  # Specific package
go test -cover ./...                      # With coverage
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

- `Initialize(...) *Module` — takes every dependency as an argument (constructor injection, no `Set*` setters). Repositories shared between modules (users, roles) are built once in [internal/router/router.go](internal/router/router.go) and passed in
- `SetupRoutes(router *gin.RouterGroup)` - Route registration

Middleware backends (authz service, API-key validator, rate limiter) are registered once in router.go via `middleware.Set*`. Package names have no underscores (`apikey`); use Go initialisms (`APIKey`, `ID`, not `ApiKey`).

### Routes

All modules are initialized in [internal/router/router.go](internal/router/router.go):

- `GET /health` (liveness, static) and `GET /ready` (pings Postgres + Redis, 503 when either is down or while draining after SIGTERM; a pool saturated by traffic counts as ready, so load never pulls every replica at once — [internal/router/ready.go](internal/router/ready.go))
- **Core**: `/core/v1/auth`, `/core/v1/account` (the caller's own sessions, email, data export, deletion — served by the auth module), `/core/v1/users`, `/core/v1/roles`, `/core/v1/api-keys`, `/core/v1/audit-logs` — contracts in [docs/api-contract/](docs/api-contract/README.md)

Every request passes panic recovery (never logs headers), the audit client context (IP, user agent), CORS from `CORS_ALLOWED_ORIGINS`, and a 2 MiB body limit.

### Access Model

1. **Middleware Chain**: `JWTAuth()` → `RequirePermission()`
2. **Roles are global**: assigned in `core.user_roles`; a user's effective permissions are the merged permissions of all their active roles
3. **Permission codes** are `<resource>:<action>`. Resources are registered in [internal/shared/authz/resources.go](internal/shared/authz/resources.go) (`users`, `roles`, `audit_logs` — read only); a role stores one level per resource: `viewer` → read; `editor` → create, read, update, delete; `admin` → editor + export, import, restore
4. **Live authz subject**: on every request `JWTAuth` loads the user's `authz.Subject` (active flag, super admin, permissions, session epoch) from Redis (`timebox:perms:user:<user_id>`, see [internal/shared/authz/](internal/shared/authz/)). Inactive/deleted users get 401 even with an unexpired token. Invalidate the cache (`InvalidateUser` / `InvalidateAll`) whenever a role, a role assignment, a user's active status or session epoch changes; version counters keep a request that read the DB just before an invalidation from writing the stale subject back
5. **Session epoch**: `users.tokens_valid_after`. Ending all of a user's sessions revokes every refresh token and gives every access token issued before that moment a 401. Logout-all, deactivation/deletion of a user and refresh-token reuse go through `AuthService.RevokeAllSessions`; a password change or reset and an account claim do it inside the transaction that changes the credentials (`revokeSessionsTx`, then `invalidateSubject` after commit). A single session is a chain of rotated refresh tokens sharing `refresh_tokens.session_id`, carried by access tokens as the `sid` claim: `/logout` and `DELETE /account/sessions/:id` revoke its refresh token and invalidate the subject, whose `EndedSessions` (sessions ended within the last access-token lifetime, loaded by the role repository) make `JWTAuth` refuse that session's access tokens. Refresh rotation (claim + new token + access token) is one transaction, so a live session never looks ended and a failure leaves the presented token usable. **Issuing or refreshing a session and ending sessions serialize on the user row:** sign-in (the hash-bound `ClearPasswordAttempts`), Google sign-in, refresh and `EndSession` lock it (`UserRepository.FindByIDForUpdate`) in the transaction that issues or revokes; `revokeSessionsTx` locks it first and takes the epoch after. Anything new that issues tokens or ends sessions does the same, or a session issued concurrently survives the revocation
6. **Never trust token claims for authorization**: `is_super_admin` and `roles` in the JWT are informational and stale until expiry. Use `middleware.IsSuperAdmin(c)` / `middleware.Caller(c)`, not `claims.IsSuperAdmin`
7. **Grant ceiling**: anything that hands out access (assigning a role, creating/editing a role, scoping an API key) or manages another user must check `caller.Covers(perms)` — a caller may only grant or manage what it holds itself
8. **API keys** (`X-API-Key: tbx_<env>_<key_id>_<secret>`) act with their scope ∩ what the owner holds now — never as super admin — and are rate limited per key. Endpoints that manage credentials, sessions or the caller's own account add `middleware.DenyAPIKeyAuth()` after `JWTAuth()`
9. **New users start with no role**: signup (email or Google) grants zero permissions until someone with `users:update` assigns a role — and only to a verified email: `PUT /users/:id` with `role_id` → 409 otherwise. An admin may vouch with `is_email_verified: true` (same request is fine), never for their own account (403)
10. **Client IP** comes from `X-Forwarded-For` only for proxies listed in `TRUSTED_PROXIES`; per-IP rate limits (`middleware.RateLimitByIP`) and API-key IP whitelists depend on it
11. **Re-authentication**: a change that could hand the account, or its data, to someone else (email change, account deletion — and any new one like it) calls `reauthenticate` first: the current password, checked under the sign-in lock, or a Firebase ID token from a Google sign-in less than 5 minutes old (`auth_time`) of a Google account linked to the caller. A session alone may be a stolen access token. The transaction that then makes the change starts with `confirmReauthTx`: if the password or email changed since the check (a reset landing in between), the change is refused
12. **An unverified email is only a claim**: whoever proves control of the address takes over an unverified account — Google sign-in with a Google-verified email, or the emailed reset link. The claim removes/replaces the password, verifies the email, clears the lock and revokes every session, API key and pending emailed link, all in the transaction that changes the account (`endPriorAccessTx` in [account.go](internal/modules/core/auth/service/account.go); the authz cache is invalidated after commit); Google linking into a verified account revokes nothing. Residual risk: if the real owner clicks a verification link they never asked for, the squatter's account becomes verified and can no longer be claimed

### RBAC Middleware

Located in [internal/middleware/](internal/middleware/):

```go
middleware.JWTAuth()                                  // Bearer JWT or X-API-Key; loads the live subject
middleware.RequirePermission("users:create")          // Has specific permission
middleware.RequireAnyPermission("roles:read", "users:create")  // Has any
middleware.RequireAllPermissions("users:read", "roles:read")   // Has all

// Session-only endpoints (credentials, sessions, the caller's own account)
middleware.DenyAPIKeyAuth()

// Unauthenticated endpoints that need throttling (Redis-backed, IPv6 per /64)
middleware.RateLimitByIP("auth:signin", limit, window)
```

`RequireRole`, `RequireAllRoles` and `OptionalAuth` no longer exist.

### Authentication Flow

1. **Sign Up**: `POST /core/v1/auth/signup` (no role; emails a verification link)
2. **Sign In**: `POST /core/v1/auth/signin` (or `/google` with a Firebase ID token) → Returns access + refresh tokens
3. **Refresh Token**: `POST /core/v1/auth/refresh` → New token pair of the same session; the old refresh token is revoked, and presenting it again more than 30s later revokes every session of the user. With `EMAIL_VERIFICATION_REQUIRED=true` an account whose email is not (or no longer) verified cannot refresh
4. **Logout**: `POST /core/v1/auth/logout` (one session, its access tokens included) or `POST /core/v1/auth/logout-all` (every session)
5. **Verify email**: `POST /core/v1/auth/verify-email {token}`; new link: `POST /core/v1/auth/verify-email/resend {email}`
6. **Reset password**: `POST /core/v1/auth/forgot-password {email}` → link; `POST /core/v1/auth/reset-password {token, new_password}` (verifies the email, lifts the lock, ends every session)
7. **Own account** (`/core/v1/account`, sessions only): `GET /sessions`, `DELETE /sessions/:id`; `POST /email {new_email, current_password | google_id_token}` → link to the new address → `POST /core/v1/auth/confirm-email-change {token}` (public; the link dies with a password change, reset or claim — `endPriorAccessTx` retires every unused link); `GET /export`; `POST /delete {current_password | google_id_token}` anonymizes the row and deletes identities, sessions, links, role assignments and API keys (`AuthService.CloseAccount`; a super admin is refused)

Access tokens are HS256 with issuer `timebox-api`, default lifetime 15m (`JWT_EXPIRATION`). JWT claims: `user_id`, `email`, `username`, `full_name`, `is_super_admin`, `roles`, `sid` (session id) — permissions are looked up from Redis, not embedded. Sign-in answers one 401 for an unknown login, a wrong password and a locked account (the owner learns of a lock by email); inactive / unverified are only reported once the password was right.

**Password lock**: sign-in and `PUT /users/me/password` (done by `AuthService.ChangePassword`) share one lock (`MAX_LOGIN_ATTEMPTS`, `ACCOUNT_LOCKOUT_DURATION`). Every password check is counted *before* the password is compared (`reservePasswordAttempt` → `UserRepository.ReservePasswordAttempt`, one atomic `UPDATE`): the attempt that reaches the limit locks the account at once, so a burst of parallel guesses gets no more checks than the limit, and a locked account refuses even the right password. A right password ends the run (`ClearPasswordAttempts`, or `ChangePassword` in the repository) — even when the sign-in is then refused as inactive or unverified — and lifts the lock only if its own attempt set it. Both are bound to the hash that was compared: a password reset or claim landing after the compare makes the right-but-old password lose. Anything new that checks a password must reserve an attempt the same way; never decide on a lock from a user row read earlier.

Emailed links are `EMAIL_VERIFICATION_URL` / `RESET_PASSWORD_URL` + `?token=`; the FE page POSTs the token. An email change link goes to the new address (`EMAIL_CHANGE_URL`, `account_tokens.new_email`) and is bound to the current one like the others. Link tokens live in `core.account_tokens` as SHA-256, single use (an atomic `UPDATE … RETURNING` inside the transaction that acts on it); a new one retires the older unused ones of that purpose. Resend and forgot-password answer the same whether or not the account exists, and send at most `MAX_VERIFICATION_EMAILS` / `MAX_PASSWORD_RESET_EMAILS` per account per hour.

## Adding New Features

### Creating a New Module

1. Create module directory: `internal/modules/{domain}/{module_name}/`
2. Create subdirectories: `domain/`, `dto/`, `handler/`, `repository/`, `service/`
3. Create `main.{module_name}.go` with `Initialize()` and `SetupRoutes()`
4. Register in [internal/router/router.go](internal/router/router.go)
5. If it has permission-checked routes, add a permission resource (below)

### Adding a Permission Resource

1. Add it to the registry in [internal/shared/authz/resources.go](internal/shared/authz/resources.go) (it then appears in `GET /core/v1/roles/permission-catalog`)
2. In that module's migration, grant it to `administrator`:
   ```sql
   UPDATE core.roles SET permissions = permissions || '{"<resource>": "admin"}' WHERE code = 'administrator';
   ```
3. Guard the routes with `RequirePermission("<resource>:<action>")`

### Module Conventions

- **Repositories** hold `*pgxpool.Pool` and run every statement through `r.q(ctx)` (= `database.Querier(ctx, r.db)`). One column-list const + one `scanX(pgx.Row)` per entity. An UPDATE/DELETE that matches no row returns `database.ErrNotFound`; services translate it
- **Updates never write back a row read outside their transaction**: either read it with a row lock inside `WithTx` (`FindByIDForUpdate`, as `UserService.Update`) or write only the changed columns (`RoleRepository.Update` with `RoleChanges`). A full-row write from an earlier read undoes a concurrent deactivation, email change or permission narrowing
- **Uniqueness** is enforced by the DB: check `database.UniqueViolation(err)` after the write (or translate it to a sentinel in the repo, like user's `ErrEmailTaken` / `ErrUsernameTaken`) — never an "exists?" query first
- **Transactions**: `database.WithTx(ctx, pool, func(ctx context.Context) error { ... })`. Repository calls made with that ctx join the transaction, across modules; a nested `WithTx` joins the outer one
- **Handlers**: `request.BindJSON` / `request.BindQuery` / `request.BindOptionalJSON` / `request.UUIDParam(c, "id")` write the 400 themselves and return false. Caller: `middleware.MustGetUserID(c)`, and `middleware.Caller(c)` for the grant ceiling. One `handleError(c, err, fallback)` per handler: an `errors.Is` switch, default `response.InternalError`
- **Audit**: security and administration changes record an `audit.Entry` through the injected `audit.Recorder` (actor, API key, IP and user agent come from ctx — `middleware.AuditClient` / `JWTAuth`). `Details` hold facts and field names, never personal data, secrets or the login someone typed; people are named by user id only
- **Lists**: the service returns `([]dto.XResponse, total int64, error)`; the handler calls `response.SuccessWithPagination(c, http.StatusOK, msg, items, params.Page, params.Limit, total)`. Query params `page` (default 1, max 100000) and `limit` (default 10, max 100); `ORDER BY created_at DESC, id DESC`

### Creating Database Migrations

```bash
make migrate-create NAME=create_customers_table MODULE=core
```

Migration conventions:

- Add indexes on foreign keys
- Use `TIMESTAMP WITH TIME ZONE` for timestamps (stored in UTC)
- Use `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
- `NOT NULL` (with a default) for flags, counters and audit timestamps the Go code reads into non-pointer fields
- **Expand/contract:** a migration keeps the previous release working, since a rolling update or a rollback runs it against the new schema — a new `NOT NULL` column gets a `DEFAULT`, and a column the previous release reads is not renamed or dropped in the same release (see `000011`)

## Important Conventions

### Error Handling

Use helpers from [internal/shared/response/](internal/shared/response/):

```go
response.Success(c, http.StatusOK, "Success", data)
response.Error(c, http.StatusBadRequest, "Validation failed", detail) // errors.detail = [detail]
response.SuccessWithPagination(c, http.StatusOK, "Users retrieved", data, page, limit, total)
response.InternalError(c, "Failed to create user", err) // 500: logs err, never sends it
```

Envelope: `{"data", "message", "meta": {"pagination"}, "errors"}`; validation failures put one entry per JSON field in `errors`. Never put `err.Error()` of an unexpected error into a response — it can carry SQL, hostnames, or other internals. Use `response.InternalError`.

### Logging

Use structured logging from [pkg/logger/](pkg/logger/):

```go
logger.Info("User created", logger.String("user_id", userID))
logger.Error("Failed to create user", logger.Err(err))
```

`ENV=production` gives JSON logs (and gin release mode); `DISCORD_ALERT_WEBHOOK` forwards entries at `DISCORD_ALERT_LEVEL` (warn or error; empty means warn) and above to Discord, so never log tokens, keys, passwords or email addresses — name people by user id. Production logs are not sampled: every access log line is kept.

### Validation

Use `binding` tags with Gin's validation:

```go
type CreateUserRequest struct {
    Email    string `json:"email" binding:"required,email,max=254"`
    Username string `json:"username" binding:"required,username"`
    Password string `json:"password" binding:"required,password"`
}
```

Custom validators in [pkg/validator/](pkg/validator/); `username` and `password` (8–72 bytes, upper + lower + digit, not a common password) are registered at startup by `validator.RegisterGinValidators()`.

Emails and usernames are stored lowercased and trimmed (a CHECK constraint enforces it). Pass every incoming value through `validator.NormalizeEmail` / `validator.NormalizeUsername` before comparing or storing it.

### Ready-made Packages

- [pkg/storage/](pkg/storage/): Cloudflare R2 over the S3 API (`Upload`, `Delete`, `PresignPut`, `PresignGet`, `PublicURL`). Build with `storage.NewR2(cfg.Storage)` when `cfg.Storage.Enabled()`
- [pkg/email/](pkg/email/): `email.EmailService` — `SendVerificationEmail` / `SendPasswordResetEmail` / `SendEmailChangeEmail` (token + validity), `SendWelcomeEmail`, `SendAccountLockedEmail`, `SendPasswordChangedEmail`, `SendEmailChangedEmail`, `SendAccountClosedEmail` — with embedded templates. SMTP via `email.NewSMTPEmailService(cfg.Email)` when `cfg.Email.Enabled()`, otherwise `NoOpEmailService`
- [internal/shared/mailer/](internal/shared/mailer/): `mailer.Async` wraps an `EmailService` so every send returns at once and runs in the background (max 8 at a time, 256 pending — beyond that dropped and logged; each SMTP delivery gives up after 30s, [pkg/email/transport.go](pkg/email/transport.go); failures logged, never returned) — requests never wait on SMTP and their timing reveals nothing. Built once by `router.NewMailer(cfg)` in `cmd/api`, injected into modules, drained by `Wait(ctx)` on shutdown. Inject it; never send synchronously from a request

- [internal/shared/audit/](internal/shared/audit/): the audit log. `audit.Open(ctx, cfg.Audit)` gives a MongoDB `Store` (queued, batch-written in the background, entries expire after `AUDIT_LOG_RETENTION`) or `Discard` without `MONGO_URI`. Built in `cmd/api`, injected as `audit.Recorder` (writing) / `audit.Reader` (the `auditlog` module, the account export), closed on shutdown. A full queue, a MongoDB outage or recording after `Close` drops entries (logged as errors); recording never fails or slows a request

`pkg/storage` is not used by a route yet.

## Common Gotchas

### Migration Issues

- Check current version with `make migrate-version`
- Use `make migrate-force V=X MODULE=core` to force version if stuck
- Core migrations live under `internal/database/migrations/core` and are tracked in `schema_migrations_core`
- A failed migration rolls back but leaves its version dirty: fix the cause, `make migrate-force V=<previous> MODULE=core`, then `make migrate-up`
- After a migration that changes the schema, update [docs/erd/core.mmd](docs/erd/core.mmd) and re-render (see [docs/erd/README.md](docs/erd/README.md))

### .env and make

- `make` loads `.env` with `-include` (a missing `.env` is fine): keep comments on their own line (text before an inline `#` becomes part of the value), write a literal `#` as `\#` and `$` as `$$`
- `config.Load` refuses to start on a value it cannot read — a non-boolean `EMAIL_VERIFICATION_REQUIRED`, a duration without a unit (`30` instead of `30m`), a non-number, an `ENV` other than development / staging / production, a `DB_SSLMODE` other than disable / require / verify-ca / verify-full (lib/pq in the migrate CLI rejects `prefer`) — instead of silently using the default
- Database credentials never go into a URL by string formatting: Go uses `DatabaseConfig.GetDSN()` (escaped), shell recipes pass `PGPASSWORD="$$DB_PASSWORD"`

### Access Control

- A caller does not change their own email, deactivate or delete themselves through `/users/:id` (`ErrChangeOwnEmail`, `ErrCannotRemoveSelf` → 403): those need only a session there, while `/account/email` and `/account/delete` re-authenticate — and the last super admin cannot remove itself
- Every non-public list/read endpoint needs its own `RequirePermission(...)` — there is no tenant scope to hide data
- Records owned by one user (e.g. API keys) must be filtered by `user_id` in the repository
- Changing a role's permissions, a user's roles, or a user's active status must invalidate the authz cache (`InvalidateAll` / `InvalidateUser`); ending a user's sessions goes through `RevokeAllSessions`
- `is_email_verified` means ownership of the email was proven (admin-created or vouched, seeded, Google, verification link, password reset). Never set it for self-signup or a user's own email change: a verified account is what Google sign-in links into without a claim, and what a role may be assigned to
- No credentials in migrations or other committed files — the initial super admin comes from `SEED_ADMIN_*` via `cmd/seed-admin`
- `JWT_SECRET` has no fallback: the API refuses to start without a ≥32-char secret in every `ENV`

### Email

- `EMAIL_VERIFICATION_REQUIRED=true` needs SMTP (`SMTP_HOST`, `SMTP_USER`, `SMTP_PASSWORD`, `SMTP_FROM_EMAIL`): the API refuses to start without it. SMTP set but invalid (`SMTP_FROM_EMAIL` not a plain address, link URLs not absolute http(s)) also stops startup; a wrong host or password only shows on the first send
- Without SMTP every email is only logged at debug level, link token included — fine for development, silent in production
- Link tokens are bound to the address they were sent to: once `users.email` differs, they are void

### Time zones

- Every timestamp is stored and handled in UTC (`TZ=UTC`, `time.Local = UTC`). A user's zone is `users.timezone`, an IANA name (default `Asia/Jakarta`, see `userDomain.DefaultTimezone`); use `time.LoadLocation(user.Timezone)` where a date or day boundary depends on the user, e.g. "today's timeboxes". Validate incoming zones with the `timezone` binding tag (the zone database is compiled in)

### Personal data

- A module that stores personal data adds it to `AuthService.ExportAccount` (the owner's data export) and erases it in `AuthService.CloseAccount` (same transaction). Audit entries are kept after an account is closed, until `AUDIT_LOG_RETENTION` expires them: they name people by id only but keep each request's IP address and user agent

### Tests

- Integration tests (`TestIntegration_*`, e.g. [account_integration_test.go](internal/modules/core/auth/service/account_integration_test.go)) skip unless `TEST_DATABASE_URL` points at a migrated database; they create and delete users, so never point it at data you care about. The audit store test needs `TEST_MONGO_URI` and drops a random database of its own. Keep both out of `.env`: `make` exports `.env`, and `make test` would run them too

### Runtime limits

- Every database session has `statement_timeout` 30s and `idle_in_transaction_session_timeout` 60s, and shows as `timebox-api` in `pg_stat_activity` ([internal/database/database.go](internal/database/database.go)). Keep slow work (bcrypt, HTTP calls) outside transactions
- SIGTERM: `/ready` turns 503 and the server keeps serving for 5s, in-flight requests get 15s, then emails and audit entries get 5s to flush — keep `terminationGracePeriodSeconds` ≥ 30. Ctrl+C skips the 5s; a second signal kills at once
- Migrations run in a one-off Job before the rollout (`SKIP_MIGRATION=true` on the Deployment; the entrypoint waits up to 300s for another replica's migration lock otherwise) — see [deployments/README.md](deployments/README.md)

### Hot Reload (Air)

- Configured in `.air.toml`
- Build errors logged to `build-errors.log`

## Testing Endpoints

```bash
# Health check / readiness
curl http://localhost:8080/health
curl http://localhost:8080/ready

# Sign in (super admin created by `make seed-admin` from SEED_ADMIN_* in .env)
curl -X POST http://localhost:8080/core/v1/auth/signin \
  -H "Content-Type: application/json" \
  -d '{"login": "<SEED_ADMIN_EMAIL>", "password": "<SEED_ADMIN_PASSWORD>"}'

# Authenticated request
curl http://localhost:8080/core/v1/users \
  -H "Authorization: Bearer YOUR_ACCESS_TOKEN"
```
