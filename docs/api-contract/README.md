# Timebox API Contract

**Last Updated:** 2026-10-24
**Base path:** `/core/v1`

The contract of the Timebox backend. The backend currently has one domain, **core** — authentication, account self-service, users, roles, API keys and the audit log — which every Timebox feature builds on. Feature modules will add their own sections here when they exist.

---

## Documents

| File | Endpoints | Covers |
|------|-----------|--------|
| [core/auth.md](core/auth.md) | 13 | Sign up, sign in (password & Google OAuth with PKCE), Google client config, refresh, logout, logout all, `/auth/me`, email verification and password reset with emailed 6-digit codes, web refresh cookie vs desktop body tokens, claiming an account with an unverified email, the `sid` claim, the user object, user time zones |
| [core/account.md](core/account.md) | 9 | The caller's own account: list and end sessions, change email (code to the new address: request, resend, confirm), link / unlink a Google account, export account data, delete account — with re-authentication |
| [core/user.md](core/user.md) | 10 | User CRUD with role assignment (verified emails only), vouching for an email, own profile (incl. time zone), change password, upload / remove avatar |
| [core/role.md](core/role.md) | 7 | Role CRUD, role permissions, permission catalog |
| [core/api-key.md](core/api-key.md) | 5 | Create, list, read, update and revoke your own API keys |
| [core/audit-log.md](core/audit-log.md) | 1 | Read the audit log (MongoDB, optional): who did what to whom, every recorded action |

Outside `/core/v1` there are two probes. They answer plain JSON, not the envelope below:

| Method | Path | Response |
|--------|------|----------|
| GET | `/health` | `200 {"status": "ok"}` — liveness: the process is up |
| GET | `/ready` | `200 {"status": "ok"}`, or `503 {"status": "unavailable"}` when PostgreSQL or Redis is unreachable, or while the instance drains after SIGTERM. A connection pool saturated by traffic still answers `200` |

---

## Response Format

Every endpoint under `/core/v1` answers with the same envelope. `meta`, `code`, `errors` and `retry_after` are left out when empty.

### Success

```json
{
  "data": { "id": "550e8400-...", "email": "rina@timebox.id" },
  "message": "User retrieved successfully"
}
```

`data` is `null` for actions without a result (e.g. delete, logout). Optional fields of an object are left out when empty, not sent as `null` — except the [user object of the auth and account endpoints](core/auth.md#user-object), which always sends every field (`full_name`, `avatar_url` and `google` as `null` when empty).

### List

```json
{
  "data": [ { "id": "550e8400-..." } ],
  "message": "Users retrieved successfully",
  "meta": {
    "pagination": {
      "page": 1,
      "limit": 10,
      "total": 23,
      "total_pages": 3
    }
  }
}
```

Every list (`GET /users`, `GET /roles`, `GET /api-keys`, `GET /audit-logs`) takes:

| Param | Default | Rule |
|-------|---------|------|
| `page` | `1` | 1 – 100000 |
| `limit` | `10` (`20` for `/audit-logs`) | 1 – 100 |

`total_pages` = ⌈`total` / `limit`⌉. Items are ordered newest first. `GET /account/sessions` is not paginated: `data` is the whole array, without `meta`.

### Errors

Every error carries a **`code`**: a stable, machine-readable reason. Clients branch on `code`, never on `message` (which is for people and may change). The full list: [Error codes](#error-codes).

```json
{
  "data": null,
  "message": "Unauthorized",
  "code": "token_expired",
  "errors": { "detail": ["Token has expired"] }
}
```

| Field | When |
|-------|------|
| `data` | `null`, except for errors that carry what the client needs to act on them: `invalid_code` → `{"attempts_left": 3}`; `email_not_verified` from sign-in → `{"email": "rina@timebox.id"}` |
| `message` | Always. Human-readable |
| `code` | Always on an error. Never on a success |
| `errors` | Field errors on `validation_failed`; otherwise `errors.detail`, an array with one string, when the error has a detail |
| `retry_after` | On a `429` (except `account_locked`): whole seconds (≥ 1) to wait, the same value as the `Retry-After` header |

**Validation (400 `validation_failed`).** `message` is `Invalid request payload` (body) or `Invalid query parameters` (query string). `errors` maps each invalid field, named as the client sent it, to its messages:

```json
{
  "data": null,
  "message": "Invalid request payload",
  "code": "validation_failed",
  "errors": {
    "email": ["must be a valid email address"],
    "password": ["must be 8-72 bytes with an uppercase letter, a lowercase letter and a number, and not a common password"]
  }
}
```

| Rule | Message |
|------|---------|
| required | `is required` |
| email | `must be a valid email address` |
| UUID | `must be a UUID` |
| URL | `must be a valid URL` |
| username | `must be 3-50 letters, digits, dots, underscores or hyphens, starting with a letter or digit` |
| password | `must be 8-72 bytes with an uppercase letter, a lowercase letter and a number, and not a common password` |
| enum | `must be one of: live test` |
| exact length | `must be exactly 6 characters` |
| digits only | `must contain only digits` |
| IP / CIDR | `must be an IP address or CIDR range` |
| timestamp | `must be an RFC 3339 timestamp, e.g. 2026-12-31T23:59:59Z` |
| timezone (IANA name, e.g. `Asia/Jakarta`) | `is invalid (timezone)` |
| min / max | `must be at least 2 characters`, `must contain at most 50 item(s)`, `must be at most 100000` |

A field with the wrong JSON type gets `["has the wrong type"]` (also `validation_failed`); a body that is not JSON gets `400 bad_request` with `errors.detail: ["Body or parameters could not be parsed"]`.

**Rate limited (429).** Header `Retry-After` and body `retry_after` give the seconds to wait:

```json
{
  "data": null,
  "message": "Too many requests. Please try again later.",
  "code": "rate_limited",
  "errors": { "detail": ["Retry after 42 seconds"] },
  "retry_after": 42
}
```

**Unexpected server errors** are `500 internal_error` with only a message — the cause is logged, never returned.

Errors any endpoint can return:

| Status | `code` | `message` | `errors.detail` | When |
|--------|--------|-----------|-----------------|------|
| 400 | `bad_request` | `Invalid id` | `must be a UUID` | Path `:id` is not a UUID |
| 413 | `payload_too_large` | `Request body too large` | `limit is 2097152 bytes` | Body larger than 2 MiB |
| 429 | `rate_limited` | `Too many requests. Please try again later.` | `Retry after N seconds` | Rate limit hit; `retry_after` and header `Retry-After` give the seconds |
| 500 | `internal_error` | depends on the endpoint | - | Unexpected error |

---

## Error Codes

Every `code` the API sends, with its status. A code never changes meaning; new ones may be added.

**Only `token_expired` means "refresh the access token and retry".** `session_ended` and `user_inactive` mean the session is over: drop the tokens and send the user to sign in. No other `401` ends a session — in particular, [re-authentication](core/account.md#re-autentikasi) errors never use `token_expired` or `session_ended`.

### Generic

| `code` | Status | Meaning |
|--------|--------|---------|
| `validation_failed` | 400 | Body or query failed validation; `errors` has one list of messages per field |
| `bad_request` | 400 | Malformed request without a more specific code: body not JSON, `:id` not a UUID, invalid `role_id`, invalid API key settings, … |
| `unauthorized` | 401 | No credential, or one that is not valid: missing / malformed `Authorization`, bad signature, unknown or revoked API key, … |
| `forbidden` | 403 | Authenticated but not allowed: missing permission, grant ceiling, protected role / user, API key on a session-only endpoint, … |
| `not_found` | 404 | Resource does not exist (or is not the caller's) |
| `conflict` | 409 | Conflict without a more specific code: role code taken, API key already revoked, role assigned to a user whose email is not verified, … |
| `payload_too_large` | 413 | Body over 2 MiB |
| `rate_limited` | 429 | Per-IP limit of a public endpoint, or an API key's own limit. `retry_after` |
| `internal_error` | 500 | Unexpected error; details are only logged |
| `service_unavailable` | 503 | A dependency is not configured, e.g. the audit log (`GET /audit-logs` without `MONGO_URI`) |

### Credential (every authenticated endpoint)

| `code` | Status | Meaning | Client action |
|--------|--------|---------|---------------|
| `token_expired` | 401 | The access token expired | `POST /auth/refresh`, then retry the request once |
| `session_ended` | 401 | The session is over: the access token's session was ended (logout, ended session, logout all, password change or reset, claimed account, stolen refresh token), or — from `/auth/refresh` — the refresh token is unknown, expired, revoked or missing | Sign in again |
| `user_inactive` | 401 | The user (or the API key's owner) was deactivated or deleted, or deleted their own account | Sign in again (it fails until reactivated) |

### Auth and account

| `code` | Status | Meaning | Where |
|--------|--------|---------|-------|
| `invalid_credentials` | 401 | Unknown login, wrong password, or locked account — deliberately one answer | `POST /auth/signin` |
| `email_not_verified` | 403 | The email is not verified and `EMAIL_VERIFICATION_REQUIRED=true`. `data.email` on sign-in | `/auth/signin`, `/auth/google`, `/auth/refresh` |
| `email_taken` | 409 | The email is used by another account | `/auth/signup`, `/account/email`, `/account/email/confirm`, `POST /users`, `PUT /users/:id` |
| `username_taken` | 409 | The username is used by another account | `/auth/signup`, `POST /users`, `PUT /users/:id` |
| `invalid_code` | 400 | Not the current emailed code — wrong, or an earlier code a newer one replaced — counted as one wrong attempt; the current code still works. `data.attempts_left` | `/auth/verify-email`, `/auth/reset-password/verify`, `/account/email/confirm` |
| `code_expired` | 400 | No code to check: the current code expired or was used, codes were retired (password change or reset, claimed account), none was sent, the address is unknown or belongs to a deactivated account (answered alike, so it never reveals which accounts exist), or the account's address changed | same |
| `too_many_attempts` | 429 | The code died after `OTP_MAX_ATTEMPTS` wrong guesses. `retry_after` (≥ 1) = seconds until a new code may be requested | same |
| `resend_cooldown` | 429 | A code went to this address (or for this email change) less than `OTP_RESEND_COOLDOWN` ago. `retry_after` | `/auth/verify-email/resend`, `/auth/forgot-password`, `/account/email`, `/account/email/resend` |
| `email_quota_exceeded` | 429 | Too many email change codes this hour (`MAX_VERIFICATION_EMAILS`). `retry_after` | `/account/email`, `/account/email/resend` |
| `invalid_reset_token` | 400 | The `reset_token` is unknown, used, expired, or for an address the account no longer has | `/auth/reset-password` |
| `email_unchanged` | 400 | The new email is the current one | `/account/email` |
| `email_change_not_pending` | 409 | No email change is pending (its code was used or expired) | `/account/email/resend` |
| `incorrect_password` | 400 | The current password is wrong (counts toward the sign-in lock) | re-authentication, `/account/google/link`, `PUT /users/me/password` |
| `no_password` | 409 | The account has no password (Google only) | same, and `DELETE /account/google` |
| `account_locked` | 429 | Too many incorrect passwords; the account is locked for `ACCOUNT_LOCKOUT_DURATION`. **No** `retry_after` / `Retry-After` | same as `incorrect_password` |
| `reauth_required` | 400 | Not exactly one of `current_password` / `google`, or the password or email changed during the request | `/account/email`, `/account/delete`, `/account/google/link` |
| `reauth_stale` | 401 | The Google sign-in used to re-authenticate is older than 5 minutes | `/account/email`, `/account/delete` |
| `google_not_configured` | 503 | The platform (`desktop` / `web`) has no Google OAuth client | `/auth/google/client`, `/auth/google`, Google re-authentication, `/account/google/link` |
| `google_auth_failed` | 401 | Google refused the authorization code (used, expired, wrong verifier or redirect URI), could not be reached, or its ID token did not verify | same, without `/auth/google/client` |
| `invalid_redirect_uri` | 400 | `redirect_uri` is not allowed for the platform | same |
| `google_email_not_verified` | 401 | First sign-in with a Google account whose email Google has not verified | `/auth/google` |
| `google_account_conflict` | 409 | The account with this Google account's email has another Google account linked | `/auth/google` |
| `google_account_not_linked` | 403 | The Google account used to re-authenticate is not linked to the caller's account | `/account/email`, `/account/delete` |
| `google_already_linked` | 409 | The caller's account has a Google account linked already | `/account/google/link` |
| `google_account_in_use` | 409 | The Google account is linked to another account | `/account/google/link` |
| `google_not_linked` | 404 | The caller's account has no Google account to unlink | `DELETE /account/google` |
| `session_not_found` | 404 | No live session of the caller has that id | `DELETE /account/sessions/:id` |
| `super_admin_cannot_close` | 409 | A super admin cannot delete their own account | `/account/delete` |
| `origin_not_allowed` | 403 | Refresh without a body token and without `X-Timebox-Client: web` plus an `Origin` in `CORS_ALLOWED_ORIGINS` | `/auth/refresh` |

### Users

| `code` | Status | Meaning | Where |
|--------|--------|---------|-------|
| `invalid_image` | 400 | The upload is not a JPEG, PNG or WebP image of 1–4096 px a side | `PUT /users/me/avatar` |
| `storage_not_configured` | 503 | No R2 storage (or no `R2_PUBLIC_URL`): avatars cannot change | `PUT` / `DELETE /users/me/avatar` |

---

## Authentication

| Header | Credential |
|--------|------------|
| `Authorization: Bearer <access_token>` | A user session, from `/auth/signin`, `/auth/google`, `/auth/verify-email`, `/auth/reset-password` or `/auth/refresh` |
| `X-API-Key: tbx_<env>_<key_id>_<secret>` | An API key ([api-key.md](core/api-key.md)). When both headers are sent, the API key is used |
| `X-Timebox-Client: web` | Not a credential: marks the browser app, which receives its refresh token as the httpOnly cookie `tb_refresh` instead of in the body ([web vs desktop](core/auth.md#klien-web-vs-desktop)) |

- **Public** (no credential, rate limited per client IP, each endpoint in its own bucket): `POST /auth/signup`, `/auth/signin`, `GET /auth/google/client`, `POST /auth/google`, `/auth/refresh`, `/auth/verify-email`, `/auth/verify-email/resend`, `/auth/forgot-password`, `/auth/reset-password/verify`, `/auth/reset-password`.
- **Session only** — an API key gets `403 forbidden` (`This endpoint cannot be used with an API key`): `POST /auth/logout`, `POST /auth/logout-all`, `GET /auth/me`, `PUT /users/me`, `PUT /users/me/password`, `PUT` / `DELETE /users/me/avatar`, every `/account` endpoint (`GET /account/sessions`, `DELETE /account/sessions/:id`, `POST /account/email`, `/account/email/resend`, `/account/email/confirm`, `POST /account/google/link`, `DELETE /account/google`, `GET /account/export`, `POST /account/delete`), and every `/api-keys` endpoint.
- **Everything else** accepts both. With an API key the request may use the key's scope, narrowed to what the key's owner holds at that moment — never super admin rights. `GET /users/me` with an API key returns the key's owner.

Access tokens live 15 minutes by default (`expires_in` in seconds); refresh before they expire, or on `401 token_expired`. Each access token carries its session id (claim `sid`), kept across refreshes. The authorization state (active flag, roles, permissions, ended sessions) is read live on every request, so role changes, deactivation and ending a session apply to the next request, not after the token expires.

`POST /account/email` and `POST /account/delete` also need [re-authentication](core/account.md#re-autentikasi) in the body: the current password, or a `google` object — the authorization code of a Google sign-in made just now (PKCE), into the Google account linked to the caller's. `POST /account/google/link` always needs the current password. A failed re-authentication has its own codes (`reauth_required`, `incorrect_password`, `reauth_stale`, `google_auth_failed`, …) and never ends the session.

### Authentication flow

| Flow | Steps |
|------|-------|
| Password | `POST /auth/signup` → 6-digit code by email → `POST /auth/verify-email {email, code}` → session. Later: `POST /auth/signin {login, password}` → session |
| Google (desktop & web) | `GET /auth/google/client?platform=desktop\|web` → `client_id` → the app runs Google's authorization code flow with PKCE (`scope=openid email profile`, `code_challenge_method=S256`; desktop redirects to `http://127.0.0.1:<port>/…`, web to a registered `redirect_uris` entry) → `POST /auth/google {code, code_verifier, redirect_uri, platform}` → session (`201` with `is_new_user: true` for a new account) |
| Forgot password | `POST /auth/forgot-password {email}` → code by email → `POST /auth/reset-password/verify {email, code}` → `reset_token` → `POST /auth/reset-password {reset_token, new_password}` → session; every other session ends |
| Change email | `POST /account/email {new_email, current_password \| google}` → code to the new address → `POST /account/email/confirm {code}` |
| Keep signed in | `POST /auth/refresh` — desktop sends `{refresh_token}` and stores the new one from the body; web sends `X-Timebox-Client: web` with `credentials: 'include'` and the cookie is rotated |
| Sign out | `POST /auth/logout` (this session), `DELETE /account/sessions/:id` (another one), `POST /auth/logout-all` (all) |

Emailed codes work `OTP_EXPIRATION` (15 min), once, die after `OTP_MAX_ATTEMPTS` (5) wrong guesses, and can be sent again after `OTP_RESEND_COOLDOWN` (60 s) — see [Kode di email](core/auth.md#kode-di-email-otp).

**Web vs desktop.** The desktop app (Electron main process, no `Origin`) keeps its refresh token itself (OS keychain) and sends it in the body. The browser app sends `X-Timebox-Client: web` on `/auth/*` requests: every endpoint that issues a session, and `/auth/refresh`, sets the refresh token as cookie `tb_refresh` (`HttpOnly`, `Path=/core/v1/auth`, `SameSite=Strict`, `Secure` outside `ENV=development`, `Max-Age` = `JWT_REFRESH_EXPIRATION`) and leaves it out of the body. A refresh through the cookie also needs an `Origin` listed in `CORS_ALLOWED_ORIGINS`; logout, logout all and account deletion clear the cookie. In production the API and the web app must be on the same site (e.g. `api.timebox.id` and `app.timebox.id`), or `SameSite=Strict` keeps the browser from sending the cookie. Details: [auth.md](core/auth.md#klien-web-vs-desktop).

**CORS.** `CORS_ALLOWED_ORIGINS` lists the browser origins allowed to call the API with credentials; allowed request headers are `Origin`, `Content-Type`, `Accept`, `Authorization`, `X-API-Key` and `X-Timebox-Client`, and `Retry-After` is exposed to scripts. A request from an origin outside the list is refused with an empty `403` (not the envelope).

### Authentication errors

| Status | `code` | `message` | `errors.detail` | When |
|--------|--------|-----------|-----------------|------|
| 401 | `unauthorized` | `Unauthorized` | `Authorization header required` | No `Authorization` and no `X-API-Key` |
| 401 | `unauthorized` | `Unauthorized` | `Invalid authorization header format. Expected: Bearer <token>` | |
| 401 | `token_expired` | `Unauthorized` | `Token has expired` | Refresh the token and retry |
| 401 | `unauthorized` | `Unauthorized` | `Invalid token signature` / `Invalid token` | |
| 401 | `session_ended` | `Unauthorized` | `Session has been revoked. Please sign in again.` | The token's session was ended — `POST /auth/logout`, or `DELETE /account/sessions/:id` — or it was issued before all of the user's sessions were ended (logout all, password change or reset, account claimed by its email's owner, deactivation, stolen refresh token) |
| 401 | `user_inactive` | `Unauthorized` | `User account is not active` | The user — or the API key's owner — was deactivated or deleted, or deleted their own account |
| 401 | `unauthorized` | `Unauthorized` | `Invalid API key format` / `Invalid API key` / `API key has been revoked` / `API key has expired` / `IP address not allowed for this API key` | API key rejected ([api-key.md](core/api-key.md#using-an-api-key)) |
| 403 | `forbidden` | `Forbidden` | `Insufficient permissions` | Missing the endpoint's permission |
| 403 | `forbidden` | `Forbidden` | `This endpoint cannot be used with an API key` | Session-only endpoint |
| 429 | `rate_limited` | `Too many requests. Please try again later.` | `Retry after N seconds` | The API key's own rate limit |

---

## Permissions

A permission code is `<resource>:<action>`, e.g. `users:read` — no module prefix. A role grants one **level** per resource, and the level expands to actions:

| Level | Actions |
|-------|---------|
| `viewer` | `read` |
| `editor` | `create`, `read`, `update`, `delete` |
| `admin` | `create`, `read`, `update`, `delete`, `export`, `import`, `restore` |

Registered resources today:

| Resource | Covers |
|----------|--------|
| `users` | User accounts and their role assignments |
| `roles` | Roles and the permissions they grant |
| `audit_logs` | The audit log — read only ([audit-log.md](core/audit-log.md)) |

The live list of resources and levels: `GET /core/v1/roles/permission-catalog` ([role.md](core/role.md#3-get-permission-catalog)).

Permissions checked by the endpoints:

| Permission | Endpoints |
|------------|-----------|
| `users:read` | `GET /users`, `GET /users/:id` |
| `users:create` | `POST /users` — and reading roles (`GET /roles`, `/roles/:id`, `/roles/permission-catalog`) |
| `users:update` | `PUT /users/:id` — and reading roles |
| `users:delete` | `DELETE /users/:id` |
| `roles:read` | `GET /roles`, `GET /roles/:id`, `GET /roles/permission-catalog` |
| `roles:create` | `POST /roles` |
| `roles:update` | `PUT /roles/:id`, `PUT /roles/:id/permissions` |
| `roles:delete` | `DELETE /roles/:id` |
| `audit_logs:read` | `GET /audit-logs` |

The `/account` endpoints need no permission: every signed-in user manages their own account. `export`, `import` and `restore` can be granted but no endpoint checks them yet, nor any `audit_logs` action other than `read`. Migration `000010` grants `audit_logs` (level `admin`) to the `administrator` role. A user holding an active `super_admin` role passes every check. Anything that hands out access (assigning a role, creating or editing a role, scoping an API key) is capped at what the caller holds.

FE: take the flat `permissions` array from any response that starts a session (`/auth/signin`, `/auth/google`, `/auth/verify-email`, `/auth/reset-password`) or from `/auth/me`, and check `permissions.includes("users:create")`.

---

## HTTP Status Codes

| Code | When |
|------|------|
| 200 OK | Successful read, update, delete, sign in |
| 201 Created | Resource created (signup, user, role, API key, first Google sign-in) |
| 400 Bad Request | Validation failed, invalid id, invalid permissions or API key settings, wrong / expired emailed code, invalid reset token, re-authentication missing or wrong password (or the password changed meanwhile), new email equal to the current one, redirect URI not allowed, unsupported avatar image |
| 401 Unauthorized | Missing, invalid, expired or revoked credential; ended session; inactive user; wrong sign-in; Google refused the authorization code; Google email not verified; Google re-authentication older than 5 minutes |
| 403 Forbidden | Missing permission, grant ceiling, protected role/user, API key on a session-only endpoint, email not verified (with `EMAIL_VERIFICATION_REQUIRED=true`), refresh from a disallowed origin, Google account not linked to the caller's account, changing your own email or deactivating / deleting yourself through `/users/:id`; an origin outside `CORS_ALLOWED_ORIGINS` (empty body) |
| 404 Not Found | Resource does not exist or was deleted (API keys and sessions: or belongs to someone else); no Google account to unlink |
| 409 Conflict | Duplicate email, username or role code; API key already revoked; role assigned to a user whose email is not verified; password re-authentication, password change or Google unlink on an account without a password; Google account already linked / linked elsewhere / conflicting with the account's email; no email change pending; super admin deleting their own account |
| 413 Payload Too Large | Body over 2 MiB |
| 429 Too Many Requests | Rate limit hit; resend cooldown; too many wrong codes; too many email change codes this hour; too many incorrect passwords (account locked) — all but the last carry `retry_after` and `Retry-After` |
| 500 Internal Server Error | Unexpected error (no details returned) |
| 503 Service Unavailable | Google sign-in not configured for the platform; avatar storage not configured; audit log not configured (`GET /audit-logs`); `/ready` when a dependency is down or the instance is draining |

---

## Example Request

```javascript
const res = await fetch(`${API_BASE_URL}/core/v1/users?page=1&limit=20`, {
  headers: { Authorization: `Bearer ${accessToken}` }
});
const body = await res.json();

if (!res.ok) {
  // Branch on body.code; body.message is for people, body.errors has field errors or errors.detail
  if (body.code === 'token_expired') {
    // refresh, then retry once
  } else if (body.code === 'session_ended' || body.code === 'user_inactive') {
    // signed out: go to the sign-in page
  }
  console.error(res.status, body.code, body.message, body.errors);
} else {
  console.log(body.data, body.meta.pagination);
}
```

---

## Changelog

### Version 2.3 (2026-10-24)
- **Breaking:** emailed links replaced by 6-digit codes. `POST /auth/verify-email {email, code}` now signs in; `/auth/verify-email/resend` and `/auth/forgot-password` answer `{expires_in, resend_available_in}` and `429 resend_cooldown`; new `POST /auth/reset-password/verify {email, code}` → `reset_token`; `POST /auth/reset-password {reset_token, new_password}` now returns a session. `POST /auth/confirm-email-change` removed: `POST /account/email/confirm {code}` and `POST /account/email/resend`
- **Breaking:** Google sign-in without Firebase — `GET /auth/google/client`, `POST /auth/google {code, code_verifier, redirect_uri, platform, timezone?}` (authorization code + PKCE, desktop and web). Re-authentication uses a `google` object instead of `google_id_token`. New `POST /account/google/link`, `DELETE /account/google`
- **Breaking:** every error carries `code`; every `429` (except `account_locked`) carries `retry_after`. Unverified email is `403 email_not_verified` (was `401`); an unusable refresh token is `401 session_ended`
- Signup: `username` optional; answers `{user, verification}`
- User object of the auth and account endpoints: `full_name` / `avatar_url` sent as `null`, new `is_email_verified` and `google`
- Web refresh cookie `tb_refresh` with `X-Timebox-Client: web`; `/auth/refresh` and `/auth/logout` take the refresh token from the body or the cookie; `/auth/logout` without one ends the access token's own session
- Avatars: `PUT` / `DELETE /users/me/avatar` (R2 + `R2_PUBLIC_URL`), `avatar_url` no longer writable; Google profile pictures are copied to R2
- Emails when a Google account is linked or unlinked

### Version 2.2 (2026-10-23)
- Sessions: access tokens carry the session id (claim `sid`), kept across refreshes. New [core/account.md](core/account.md): `GET /account/sessions`, `DELETE /account/sessions/:id`
- **Behaviour change:** `POST /auth/logout` now also refuses that session's access tokens from the next request (`401 Session has been revoked. Please sign in again.`); before, they lived until expiry
- `POST /auth/refresh` keeps the session; with `EMAIL_VERIFICATION_REQUIRED=true` an unverified email gets `401 Email not verified` and the presented refresh token is consumed
- Email change: `POST /account/email` (re-authenticated) sends a link to the new address, confirmed through the public `POST /auth/confirm-email-change`
- `GET /account/export` (account data as JSON) and `POST /account/delete` (re-authenticated; erases personal data, super admin → `409`)
- Re-authentication with the current password counts toward the sign-in lockout
- Per-user `timezone` (IANA name, default `Asia/Jakarta`) in user objects; optional on signup, Google sign-in (new accounts), `POST /users`, `PUT /users/:id`, `PUT /users/me`
- Audit log in MongoDB (optional, `MONGO_URI`): [core/audit-log.md](core/audit-log.md), `GET /audit-logs` with the new permission `audit_logs:read`
- Emails on email change (to the new address), email changed (to the old address) and account deleted

### Version 2.1 (2026-10-23)
- Email verification and password reset by emailed link: `POST /auth/verify-email`, `/auth/verify-email/resend`, `/auth/forgot-password`, `/auth/reset-password` (public). Signup now emails a verification link
- An account whose email is unverified is claimed by whoever proves control of the address (Google sign-in or the reset link): its password is replaced, its sessions and API keys are revoked. Google sign-in no longer answers `409` for such an account
- `PUT /users/:id`: `role_id` needs a verified email (`409` otherwise); new field `is_email_verified` lets an admin vouch for someone else's address
- Emails on account lock, password change or reset, and first verification

### Version 2.0 (2026-10-23)
- Contract rewritten for Timebox. It documents only the core module (auth, users, roles, API keys); earlier versions described modules of the codebase Timebox was forked from, which do not exist here
- Permission codes are `<resource>:<action>` with resources `users` and `roles`; `user-management` is now `users`
- Added [core/api-key.md](core/api-key.md) and `GET /roles/permission-catalog`
- `GET /api-keys` returns `data` as an array with `meta.pagination`, like every other list
- Error envelope: `errors.detail` is an array; validation errors are keyed by field
- Access tokens default to 15 minutes; ending a user's sessions also rejects their access tokens

---

**Document Version:** 2.3
**Last Updated:** 2026-10-24
