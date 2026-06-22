# Authentication API Contract

**Version:** v1
**Base URL:** `/core/v1/auth`
**Module:** Core Authentication
**Last Updated:** 2026-05-16

---

## 🆕 (2026-06-18) — Access-token revocation + active session management

Dua perubahan keamanan:

1. **Access token sekarang bisa di-revoke saat logout.** Sebelumnya `POST /auth/logout` cuma mencabut refresh token; access token (JWT) tetap valid sampai expiry alami (s/d 24 jam). Sekarang:
   - JWT membawa claim baru `jti` (token id unik per token).
   - `POST /auth/logout` mendenylist `jti` access token yang dipakai → token itu langsung **401 "Token has been revoked"** di request berikutnya.
   - `POST /auth/logout-all` menandai cutoff per-user → **semua** access token yang diterbitkan sebelum panggilan itu langsung ditolak.
   - Denylist disimpan di Redis dengan TTL = sisa umur token (auto-expire, tidak ada unbounded growth). **Fail-open**: kalau Redis down, check di-skip (token tetap jalan) demi menghindari lockout massal.
   - **Dampak FE:** tidak ada perubahan request. Cukup tangani `401` setelah logout sebagai "sesi berakhir" (memang itu yang diinginkan). Jangan lagi pakai access token setelah logout.

2. **Endpoint manajemen sesi (Keycloak-style).** Lihat [section 6 & 7](#6-list-active-sessions) — `GET /auth/sessions` untuk lihat device aktif, `DELETE /auth/sessions/:id` untuk cabut satu sesi.

3. **`JWT_REFRESH_EXPIRATION` .** Sebelumnya nilai env ini diabaikan (refresh TTL hardcoded 7 hari). Sekarang refresh-token lifetime dibaca dari env (default tetap `168h`). Tidak ada perubahan FE.

---

## 🆕 (2026-05-16) — Sign in with Google (Firebase)

Endpoint baru: `POST /core/v1/auth/google` — autentikasi pakai Firebase Google ID token. FE tinggal kirim `id_token` yang didapat dari Firebase Web SDK, BE memverifikasi, lalu salah satu dari:

- **Returning user** (identity Google sudah ter-link) → sign in normal.
- **Returning user via email match** (sudah punya akun email/password dengan email yang sama) → identity Google otomatis di-link ke akun existing, sign in normal. Tidak ada company baru yang dibuat.
- **First-time Google user** → BE otomatis bikin: user + client + company (nama company = nama profile Google) + branch default "Cabang Pusat". Response berisi `is_new_user: true` dan HTTP `201 Created`.

FE tidak perlu form "company name" terpisah untuk flow Google — semuanya auto-provisioned. Detail lengkap di [section 3 Sign In with Google](#3-sign-in-with-google).

---

## ⚠️ Breaking Change (2026-04-20) — JWT no longer carries `permissions`

Sebelumnya JWT access token berisi array `permissions` yang ~3.8 KB di kasus user dengan banyak permission → sering kena block ingress (HTTP 400 Bad Request).

Mulai sekarang:

- **JWT tidak lagi berisi field `permissions`.** Permissions disimpan di Redis dan di-lookup backend saat setiap request masuk.
- **Response body TETAP return `permissions`** di `/signin` dan `/switch-company` — FE ambil dari sana.
- **`/refresh` TIDAK mengubah permissions** (user & company sama). FE cukup pakai permissions yang sudah disimpan dari signin/switch-company sebelumnya.

### Yang perlu FE lakukan

| Skenario lama | Skenario baru |
|---|---|
| FE decode JWT → ambil `claims.permissions` | FE baca `response.data.permissions` dari signin / switch-company |
| Setelah `switch-company`, FE decode JWT baru buat update UI | FE baca `response.data.permissions` dari switch-company (field ini **baru ditambah**) |
| FE decode JWT setelah `refresh` | Tidak perlu — permissions tidak berubah saat refresh |

**Cara check permission di FE:** `response.data.permissions.includes("finance.contacts:read")` (tidak berubah — polanya sama, cuma sumbernya beda).

**Apa yang masih bisa di-decode dari JWT:** `user_id`, `company_id`, `company_name`, `client_id`, `client_slug`, `email`, `username`, `full_name`, `is_super_admin`, `roles`, `exp`, `iat`.

---

## Overview

Authentication API untuk registrasi, login, token management, dan multi-tenant company switching. Menggunakan JWT + refresh token.

**Key Features:**
- JWT-based authentication dengan refresh token rotation
- Multi-device session management
- Multi-tenant company switching
- Account lockout protection (5x gagal login = lock 15 menit)
- Server-side permission cache di Redis (ganti JWT-embedded permissions)

---

## Response Format

Semua response mengikuti format:

```json
{
  "data": { ... },
  "message": "Success message",
  "meta": null,
  "errors": null
}
```

Error response:

```json
{
  "data": null,
  "message": "Error message",
  "meta": null,
  "errors": "detail error string"
}
```

---

## JWT Claims Structure

Access token berisi **identity claims saja** — permissions tidak ada di sini (lihat Breaking Change di atas).

```json
{
  "user_id": "550e8400-...",
  "email": "user@example.com",
  "username": "johndoe",
  "full_name": "John Doe",
  "company_id": "660e8400-...",
  "company_name": "PT Tuai Indonesia",
  "client_id": "770e8400-...",
  "client_slug": "tuai",
  "is_super_admin": false,
  "roles": ["administrator"],
  "jti": "9f1c2e7a-...",
  "exp": 1735689600,
  "iat": 1735603200,
  "nbf": 1735603200,
  "iss": "tuai-api",
  "sub": "550e8400-..."
}
```

### Penjelasan Claims

| Claim | Type | Keterangan |
|-------|------|------------|
| `user_id` | string (UUID) | ID user (juga duplikat di `sub`) |
| `email` | string | Email user |
| `username` | string | Username |
| `full_name` | string | Nama lengkap |
| `company_id` | string (UUID) | Company aktif. `""` jika user belum switch company |
| `company_name` | string | Nama company aktif. `""` jika belum switch |
| `client_id` | string (UUID) | Registration-level tenant ID (parent dari company) |
| `client_slug` | string | DNS-safe client slug — dipakai FE untuk bootstrap translation overrides |
| `is_super_admin` | bool | `true` = bypass semua permission check di backend |
| `roles` | string[] | Role codes aktif user (mis. `["administrator"]`) |
| `jti` | string (UUID) | Token id unik per access token. Dipakai backend untuk denylist saat logout — FE tidak perlu memprosesnya |
| `exp` | int64 | Expiry (Unix timestamp, default 24 jam) |
| `iat` | int64 | Issued-at (Unix timestamp) |
| `nbf` | int64 | Not-before (Unix timestamp) |
| `iss` | string | Selalu `"tuai-api"` |
| `sub` | string | Subject = `user_id` |

> **Catatan:** Jangan bergantung pada urutan field. Field yang tidak kamu pakai boleh diabaikan.

### Permissions (di response body, bukan di JWT)

Permissions sekarang dikirim lewat **response body** pada endpoint berikut:
- `POST /signin` → `data.permissions`
- `POST /switch-company` → `data.permissions`

Format string tidak berubah: flat array `"resource:action"`. Contoh:
```json
[
  "finance.contacts:read",
  "finance.contacts:create",
  "user-management:read"
]
```

**FE check:** `response.data.permissions.includes("finance.contacts:create")`

### Cara permissions di-derive

Di database, role menyimpan permissions format **level-based**:
```json
{"dashboard": "viewer", "user-management": "admin"}
```

Backend expand level ke actions:
- `viewer` → `["read"]`
- `editor` → `["create", "read", "update", "delete"]`
- `admin` → `["create", "read", "update", "delete", "export", "import", "restore", "approve"]`

Sehingga response-nya flat: `["dashboard:read", "user-management:create", "user-management:read", ...]`. FE tidak perlu tahu level — cukup pakai flat array.

### Invalidation / kapan permissions berubah

Setelah signin/switch-company, permissions **tetap valid sampai** salah satu dari:
1. Admin edit permissions role yang dipegang user
2. Admin assign/remove role user
3. User logout

Kalau FE dapet **403 Forbidden** untuk aksi yang sebelumnya diizinkan → kemungkinan permissions telah berubah. Rekomendasi handling: logout user dan minta login ulang (agar dapet list permissions terbaru).

---

## Endpoints

### 1. Sign In

Autentikasi user dengan email/username dan password.

```
POST /core/v1/auth/signin
```

**Auth:** Tidak diperlukan

**Request:**
```json
{
  "login": "admin@tuai.com",
  "password": "admin123"
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `login` | string | Ya | Email atau username |
| `password` | string | Ya | Min 8 karakter |

**Response (200):**
```json
{
  "data": {
    "access_token": "eyJhbGci...",
    "refresh_token": "a1b2c3d4e5...",
    "token_type": "Bearer",
    "expires_in": 86400,
    "user": {
      "id": "550e8400-...",
      "email": "admin@tuai.com",
      "username": "admin",
      "full_name": "Admin Tuai"
    },
    "company": {
      "id": "660e8400-...",
      "name": "PT Tuai Indonesia"
    },
    "client": {
      "id": "770e8400-...",
      "slug": "tuai",
      "name": "Tuai"
    },
    "roles": ["administrator"],
    "permissions": [
      "finance.contacts:read",
      "finance.contacts:create",
      "user-management:read"
    ]
  },
  "message": "Sign in successful"
}
```

**Response fields:**

| Field | Type | Keterangan |
|-------|------|------------|
| `access_token` | string | JWT. Pakai di header `Authorization: Bearer <token>` |
| `refresh_token` | string | Opaque token 32 byte. Simpan aman (HttpOnly cookie / secure storage) |
| `token_type` | string | Selalu `"Bearer"` |
| `expires_in` | int | Lifetime access_token dalam detik (default 86400 = 24 jam) |
| `user` | object | Info user signed-in |
| `company` | object \| `null` | Primary company user. `null` jika user belum punya company |
| `client` | object \| `null` | Registration-level tenant (parent dari company). `null` jika belum ada company |
| `roles` | string[] | Role codes user di primary company (mis. `["administrator"]`) |
| `permissions` | string[] | **Flat permission list** — FE pakai untuk gating UI |

> `company`, `client` bisa `null` jika user belum punya primary company. Dalam case ini, `roles` dan `permissions` akan kosong `[]` atau berisi role global saja.

**Errors:**

| Status | Message | Kapan |
|--------|---------|-------|
| 400 | Invalid request payload | Payload tidak valid |
| 401 | Invalid credentials | Email/password salah |
| 401 | User account is not active | `is_active = false` |
| 401 | User account is locked | Terkunci setelah 5x gagal |

---

### 2. Sign Up

Registrasi user baru. Otomatis membuat company dan cabang default "Cabang Pusat".

```
POST /core/v1/auth/signup
```

**Auth:** Tidak diperlukan

**Request:**
```json
{
  "email": "newuser@example.com",
  "username": "johndoe",
  "password": "SecurePass123",
  "full_name": "John Doe",
  "phone": "+6281234567890",
  "company_name": "PT Contoh Sejahtera"
}
```

| Field | Type | Required | Validasi |
|-------|------|----------|----------|
| `email` | string | Ya | Format email valid |
| `username` | string | Ya | 3-100 karakter |
| `password` | string | Ya | Min 8 karakter |
| `full_name` | string | Tidak | Max 255 karakter |
| `phone` | string | Tidak | Max 20 karakter |
| `company_name` | string | Ya | 2-255 karakter |

**Response (201):**
```json
{
  "data": {
    "message": "User registered successfully. Please verify your email.",
    "user": {
      "id": "550e8400-...",
      "email": "newuser@example.com",
      "username": "johndoe",
      "full_name": "John Doe"
    },
    "company": {
      "id": "660e8400-...",
      "name": "PT Contoh Sejahtera"
    }
  },
  "message": "User registered successfully. Please verify your email."
}
```

> **Auto-provisioning saat registrasi:**
> 1. User dibuat dengan `is_active=true`, `is_email_verified=false`
> 2. Company dibuat dengan type `holding`, owner = user baru
> 3. User di-link ke company sebagai primary member (`is_primary=true`)
> 4. Branch "Cabang Pusat" dibuat sebagai default branch (`is_default=true`)

**Errors:**

| Status | Message |
|--------|---------|
| 400 | Invalid request payload |
| 409 | Email already exists |
| 409 | Username already exists |

---

### 3. Sign In with Google

Autentikasi (sign in **atau** sign up) pakai Firebase Google ID token. Satu endpoint, tiga skenario yang dibedakan lewat field `is_new_user` di response.

```
POST /core/v1/auth/google
```

**Auth:** Tidak diperlukan

#### Cara FE dapat `id_token`

FE pakai Firebase Web SDK (atau iOS/Android SDK). Setelah `signInWithPopup`, ambil ID token dari user:

```js
import { getAuth, signInWithPopup, GoogleAuthProvider } from 'firebase/auth'

const auth = getAuth(firebaseApp)
const result = await signInWithPopup(auth, new GoogleAuthProvider())
const idToken = await result.user.getIdToken()

// Kirim id_token ke BE:
await fetch('/core/v1/auth/google', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ id_token: idToken })
})
```

> **Jangan kirim Google access token / OAuth code** — BE hanya memverifikasi Firebase ID token (JWT yang dikeluarkan Firebase, bukan Google langsung). Project ID Firebase di FE harus sama dengan yang dipasang di BE.

#### Request

```json
{
  "id_token": "eyJhbGciOiJSUzI1NiIs..."
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `id_token` | string | Ya | Firebase ID token (JWT). Diperoleh FE dari `result.user.getIdToken()` |

#### Response (200 — returning user) atau (201 — first-time sign up)

```json
{
  "data": {
    "access_token": "eyJhbGci...",
    "refresh_token": "a1b2c3d4e5...",
    "token_type": "Bearer",
    "expires_in": 86400,
    "is_new_user": true,
    "user": {
      "id": "550e8400-...",
      "email": "john.doe@gmail.com",
      "username": "john.doe",
      "full_name": "John Doe"
    },
    "company": {
      "id": "660e8400-...",
      "name": "John Doe"
    },
    "client": {
      "id": "770e8400-...",
      "slug": "john-doe",
      "name": "John Doe"
    },
    "roles": ["administrator"],
    "permissions": [
      "finance.contacts:read",
      "finance.contacts:create",
      "user-management:read"
    ]
  },
  "message": "Account created and signed in with Google"
}
```

#### Response Fields

Sama persis dengan `/signin` ditambah satu field baru:

| Field | Type | Keterangan |
|-------|------|------------|
| `is_new_user` | bool | `true` jika request ini bikin akun baru (full auto-provisioning di BE). `false` jika user sudah ada (entah identity Google sudah ter-link, atau user existing email/password yang baru saja di-link otomatis ke Google) |
| `access_token` | string | JWT — pakai di header `Authorization: Bearer <token>` |
| `refresh_token` | string | Refresh token (32 byte opaque) — simpan aman |
| `token_type` | string | Selalu `"Bearer"` |
| `expires_in` | int | Lifetime access_token (detik, default 86400) |
| `user` | object | Info user — untuk first-time, `username` di-derive dari email local-part (mis. `john.doe@gmail.com` → `john.doe`), collisions di-resolve dengan suffix random |
| `company` | object \| `null` | Untuk first-time, otomatis dibuat dengan `name = profile.name` dari Google. Untuk returning user, primary company existing |
| `client` | object \| `null` | Sama seperti `/signin` |
| `roles` | string[] | Roles user di primary company |
| `permissions` | string[] | Flat permission list (sama format dengan `/signin`) |

#### Status codes

| Status | Kapan |
|--------|-------|
| 200 | Returning user — sign in sukses |
| 201 | First-time Google user — akun baru di-provision + sign in |

#### Skenario internal (FE tidak perlu tahu, tapi berguna untuk debugging)

| Kondisi BE | `is_new_user` | Akibatnya |
|---|---|---|
| `user_identities` row sudah ada untuk (google, firebase_uid) | `false` | Identity di-refresh, user existing sign in. Tidak ada company baru |
| Belum ada identity, tapi `core.users.email` sudah match | `false` | Identity Google **di-link otomatis** ke user existing. Tidak ada company baru. User existing sign in |
| Belum ada user sama sekali | `true` | BE auto-provision: `core.users` + `core.user_identities` + `core.clients` + `core.companies` (nama = `profile.name` Google) + `core.branches` ("Cabang Pusat"). User di-assign sebagai admin company baru |

> **Untuk first-time Google sign up — company name otomatis = nama profile Google user.** Tidak ada form input company name di FE. Kalau user ingin rename company, lakukan via endpoint company update yang biasa.

#### Errors

| Status | Message | Kapan |
|--------|---------|-------|
| 400 | Invalid request payload | Body tidak ada `id_token` |
| 401 | Invalid Google ID token | Firebase menolak token: signature salah, expired, audience salah, atau revoked |
| 401 | Unexpected sign-in provider | Token Firebase valid tapi `firebase.sign_in_provider` bukan `google.com` (mis. token dari email-link / custom auth yang sama-sama enable di project Firebase). Endpoint ini khusus Google |
| 401 | Google account did not return an email | Token Firebase tidak punya claim `email`. Defensive guard — Google selalu return email kalau scope-nya benar |
| 401 | User account is not active | User pernah ada tapi `is_active=false` atau soft-deleted |
| 503 | Google sign-in is not configured | Operator BE belum set `FIREBASE_PROJECT_ID`. FE bisa tampilkan toast "Login Google sementara tidak tersedia" |

#### FE handling rekomendasi

```ts
const res = await fetch('/core/v1/auth/google', { ... })
const json = await res.json()

if (!res.ok) {
  // tampilkan json.message ke user (sudah ramah)
  return showError(json.message)
}

const { access_token, refresh_token, permissions, is_new_user, company } = json.data
storeTokens(access_token, refresh_token)
setPermissions(permissions)

if (is_new_user) {
  // optional: tampilkan welcome screen, atau redirect ke
  // /onboarding kalau mau user rename company default-nya.
  router.push('/welcome')
} else {
  router.push('/dashboard')
}
```

---

### 4. Refresh Token

Generate access token baru menggunakan refresh token.

```
POST /core/v1/auth/refresh
```

**Auth:** Tidak diperlukan

**Request:**
```json
{
  "refresh_token": "a1b2c3d4e5..."
}
```

**Response (200):**
```json
{
  "data": {
    "access_token": "eyJhbGci...",
    "refresh_token": "z9y8x7w6...",
    "token_type": "Bearer",
    "expires_in": 86400
  },
  "message": "Token refreshed successfully"
}
```

> **Token Rotation:** Setiap refresh menghasilkan refresh token BARU. Token lama otomatis di-revoke.

**Errors:**

| Status | Message |
|--------|---------|
| 401 | Invalid refresh token |
| 401 | Refresh token expired |
| 401 | Refresh token revoked |

---

### 5. Logout (Single Device)

Revoke refresh token **dan** access token yang sedang dipakai. Access token yang dipakai di header `Authorization` langsung masuk denylist (via `jti`) → tidak bisa dipakai lagi setelah ini.

```
POST /core/v1/auth/logout
```

**Auth:** `Authorization: Bearer <access_token>`

**Request:**
```json
{
  "refresh_token": "a1b2c3d4e5..."
}
```

**Response (200):**
```json
{
  "data": null,
  "message": "Logged out successfully"
}
```

> **Sejak 2026-06-18:** access token yang dipakai untuk memanggil endpoint ini langsung di-revoke. Request berikutnya dengan token itu akan dapat `401 "Token has been revoked"`. FE harus buang access token + refresh token dari storage setelah logout.

---

### 6. Logout All Devices

Revoke semua refresh token milik user **dan** semua access token yang diterbitkan sebelum panggilan ini (lewat cutoff per-user).

```
POST /core/v1/auth/logout-all
```

**Auth:** `Authorization: Bearer <access_token>`

**Request:** Body kosong

**Response (200):**
```json
{
  "data": null,
  "message": "Logged out from all devices successfully"
}
```

> **Sejak 2026-06-18:** semua access token lama (termasuk yang dipakai memanggil endpoint ini) langsung invalid. Cocok untuk skenario "saya rasa akun saya dibajak".

---

### 6.1 List Active Sessions

Daftar sesi aktif (non-revoked) milik caller — buat layar "perangkat saya". `token_hash` **tidak pernah** dibocorkan; hanya metadata device.

```
GET /core/v1/auth/sessions
```

**Auth:** `Authorization: Bearer <access_token>`

**Query params (opsional):**

| Param | Type | Keterangan |
|-------|------|------------|
| `current_refresh_token` | string | Kalau FE mengirimkan refresh token-nya saat ini, sesi yang cocok ditandai `is_current: true` ("perangkat ini"). Opsional — kalau tidak dikirim, tidak ada sesi yang ditandai current. Tidak pernah di-log. |

**Response (200):**
```json
{
  "data": [
    {
      "id": "11111111-...",
      "device_info": { "name": "Chrome on macOS", "type": "desktop", "os": "macOS", "browser": "Chrome" },
      "ip_address": "103.xx.xx.xx",
      "last_used_at": "2026-06-18T09:00:00Z",
      "created_at": "2026-06-10T08:00:00Z",
      "is_current": true
    }
  ],
  "message": "Sessions retrieved successfully"
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `id` | string (UUID) | Session id — dipakai untuk revoke (endpoint 6.2) |
| `device_info` | object | Metadata device (name/type/os/browser), bisa kosong |
| `ip_address` | string\|null | IP saat sesi dibuat |
| `last_used_at` | string\|null | Terakhir refresh dipakai |
| `created_at` | string | Kapan sesi dibuat |
| `is_current` | bool | `true` kalau ini sesi dari refresh token yang dikirim di `current_refresh_token` |

---

### 6.2 Revoke a Session

Cabut satu sesi milik caller berdasarkan id. Hanya bisa mencabut sesi sendiri — id milik user lain (atau id tidak dikenal) mengembalikan **404** (bukan 403) supaya id tidak bisa ditebak-tebak.

```
DELETE /core/v1/auth/sessions/:id
```

**Auth:** `Authorization: Bearer <access_token>`

**Response (200):**
```json
{
  "data": null,
  "message": "Session revoked successfully"
}
```

**Errors:**

| Status | Message | Kapan |
|--------|---------|-------|
| 404 | Session not found | Id bukan milik caller, tidak ada, atau sudah ter-revoke |

> **Catatan:** revoke sesi menghentikan **refresh** berikutnya untuk device itu. Access token yang sudah terlanjur dipegang device tersebut masih jalan sampai expiry alami — untuk mematikannya seketika gunakan `logout` / `logout-all` (denylist access token).

---

### 7. Switch Company

Pindah konteks company aktif. Menghasilkan JWT baru dengan `company_id` dan `roles` sesuai company target. Permissions dikirim di response body (tidak lagi di JWT).

```
POST /core/v1/auth/switch-company
```

**Auth:** `Authorization: Bearer <access_token>`

**Request:**
```json
{
  "company_id": "660e8400-..."
}
```

**Response (200):**
```json
{
  "data": {
    "access_token": "eyJhbGci...",
    "refresh_token": "z9y8x7w6...",
    "token_type": "Bearer",
    "expires_in": 86400,
    "company": {
      "id": "660e8400-...",
      "name": "PT Tuai Indonesia"
    },
    "roles": ["manager"],
    "permissions": [
      "finance.contacts:read",
      "finance.cash_transactions:read",
      "finance.cash_transactions:create"
    ]
  },
  "message": "Company switched successfully"
}
```

> **Baru sejak 2026-04-20:** field `roles` dan `permissions` kini dikirim di response body. FE wajib **replace** permissions yang disimpan sebelumnya dengan nilai dari response ini — tidak lagi bisa didapat dari decode JWT.

**Response fields:**

| Field | Type | Keterangan |
|-------|------|------------|
| `access_token` | string | JWT baru dengan `company_id` target |
| `refresh_token` | string | Refresh token baru (token rotation) |
| `token_type` | string | Selalu `"Bearer"` |
| `expires_in` | int | Lifetime access_token (detik) |
| `company` | object | Company target |
| `roles` | string[] | Role codes user di company target. Bisa beda dari primary company |
| `permissions` | string[] | Flat permissions di company target. **Wajib FE refresh state permissions** dengan nilai ini |

**Errors:**

| Status | Message | Kapan |
|--------|---------|-------|
| 403 | You are not a member of this company | User bukan member |
| 404 | Company not found | Company tidak ada |

---

### 8. Get Me (Profile + Effective Permissions)

Ambil profile user yang sedang login beserta **effective permissions** untuk company aktif. Permissions dibaca dari Redis (authz cache), bukan dari decode JWT.

```
GET /core/v1/auth/me
```

**Auth:** `Authorization: Bearer <access_token>`

**Response (200):**
```json
{
  "data": {
    "user": {
      "id": "550e8400-...",
      "email": "admin@tuai.com",
      "username": "admin",
      "full_name": "Admin Tuai"
    },
    "company": {
      "id": "660e8400-...",
      "name": "PT Tuai Indonesia"
    },
    "client": {
      "id": "770e8400-...",
      "slug": "tuai",
      "name": "Tuai"
    },
    "roles": ["administrator"],
    "permissions": [
      "finance.contacts:read",
      "finance.contacts:create",
      "user-management:read"
    ],
    "is_super_admin": false
  },
  "message": "Profile retrieved successfully"
}
```

> `company` dan `client` bisa `null` jika user belum switch company.

**Kapan FE memanggil endpoint ini:**

1. **Setelah page reload** — untuk re-hydrate permissions state tanpa simpan di localStorage (data sensitif)
2. **Setelah dapet 403 tak terduga** — untuk cek apakah permissions user telah berubah (admin revoke role misal). Kalau `permissions` turun, update state & tampilkan notif. Kalau tetap sama, berarti memang user tidak punya akses.
3. **Periodik (opsional)** — kalau app-mu butuh real-time awareness terhadap perubahan permissions, poll `/me` tiap N menit. Tapi untuk kebanyakan app, cara (1) dan (2) sudah cukup.

**Perbedaan dari signin/switch-company:**

| | `/signin` | `/switch-company` | `/me` |
|---|---|---|---|
| Return tokens? | ✅ ya | ✅ ya | ❌ tidak |
| Mengubah server state? | ✅ ya | ✅ ya | ❌ read-only |
| Bisa dipanggil berulang? | ❌ (butuh password) | ❌ (butuh company_id baru) | ✅ unlimited |
| Source permissions? | DB + isi cache | DB + isi cache | Redis cache (fallback DB) |

**Errors:**

| Status | Message | Kapan |
|--------|---------|-------|
| 401 | User not active | User di-deactivate setelah JWT di-issue |
| 401 | Authentication required | Token missing/invalid/expired |

---

### 9. Get My Companies

Ambil daftar company yang user login punya akses. Digunakan FE untuk menampilkan branch tree, company switcher, dll.

```
GET /core/v1/auth/companies
```

**Auth:** `Authorization: Bearer <access_token>`

**Response (200):**
```json
{
  "data": [
    {
      "id": "20000000-...",
      "name": "PT Tuai Indonesia",
      "type": "holding",
      "logo_url": "https://cdn.example.com/logos/tuai-id.png",
      "parent_id": null,
      "is_primary": true,
      "is_owner": true,
      "role_name": "Super Admin",
      "role_code": "super_admin"
    },
    {
      "id": "20000001-...",
      "name": "Tuai Jakarta",
      "type": "subsidiary",
      "logo_url": null,
      "parent_id": "20000000-...",
      "is_primary": false,
      "is_owner": false,
      "role_name": "Manager",
      "role_code": "manager"
    }
  ],
  "message": "Companies retrieved successfully"
}
```

**Field notes:**
- `type` — `holding` atau `subsidiary` (lihat enum `core.company_type`).
- `parent_id` — `null` untuk holding/root company.
- `is_primary` — menandai company default user (hanya satu per user). FE bisa auto-select ini setelah login.
- `is_owner` — `true` jika user adalah `owner_id` dari company tersebut.
- `role_name` / `role_code` — peran user di company ini (bisa `null` jika membership belum di-assign role).

> Hanya company dimana user terdaftar sebagai member (via `company_users`). Super admin yang terdaftar di 2 company hanya lihat 2 company tersebut — bukan semua company di sistem.
> Urutan: `is_primary DESC`, lalu `name ASC`.

---

## Authentication Flow

```
Email/Password:
  1. POST /auth/signup            → Registrasi + auto-create company & branch "Cabang Pusat"
  2. POST /auth/signin            → Login, dapat token + company + roles + permissions

Google (Firebase):
  1. FE: signInWithPopup(GoogleAuthProvider) → idToken = user.getIdToken()
  2. POST /auth/google { id_token } → BE verify + sign in
     - is_new_user=true: BE auto-provision company (nama = profile.name Google) + branch default
     - is_new_user=false: returning user / existing email auto-linked

Common:
  3. Semua request pakai header: Authorization: Bearer <access_token>
  4. GET  /auth/me                → (Opsional) Rehydrate profile+permissions setelah reload
  5. GET  /auth/companies         → Ambil daftar company yang bisa diakses
  6. POST /auth/switch-company    → (Opsional) Pindah company, dapat token + permissions baru
  7. POST /auth/refresh           → Saat access_token expired, tukar refresh_token
  8. POST /auth/logout            → Logout dari device ini
```

### Token Expiry

| Token | Default Expiry | Env |
|-------|---------------|-----|
| Access token | 24 jam | `JWT_EXPIRATION` |
| Refresh token | 7 hari | `JWT_REFRESH_EXPIRATION` |

Keduanya dikonfigurasi via env (format Go duration, mis. `24h`, `168h`). `expires_in` di response signin/switch-company/refresh selalu mengikuti `JWT_EXPIRATION` aktual, jadi tidak pernah drift dari umur token sebenarnya.

**Revocation:** setelah `logout` / `logout-all`, access token langsung invalid (denylist Redis, fail-open saat Redis down) — tidak menunggu expiry alami. Lihat changelog 2026-06-18 di atas.

---

## FE Integration Checklist (migrasi dari JWT-embedded permissions)

Kalau FE kamu sebelumnya pakai permissions dari JWT, berikut langkah migrasinya:

- [ ] Stop decode JWT untuk ambil `permissions`. Hapus semua `jwtDecode(token).permissions`.
- [ ] Simpan `response.data.permissions` dari `/signin` ke state/store FE (Pinia, Redux, dst).
- [ ] Pada `/switch-company`, **update** state permissions dengan `response.data.permissions` (field ini baru ditambahkan).
- [ ] Pada `/refresh`, **tidak perlu update** permissions — user & company sama, permissions tidak berubah.
- [ ] Pada **page reload**, call `GET /auth/me` untuk rehydrate permissions (kalau tidak disimpan di persistent storage). Kalau user sudah punya token di memory, `/me` lebih aman daripada simpan permissions di localStorage.
- [ ] Kalau dapet **403 tak terduga**, call `GET /auth/me` dulu untuk cek apakah permissions telah berubah. Kalau berubah (admin revoke), update state + show toast "Your permissions have changed". Kalau tidak berubah, artinya user memang tidak punya akses — biarkan error handler normal jalan.
- [ ] Cek ukuran request header setelah deploy — harusnya drop dari ~4 KB ke < 1 KB, gak akan kena block ingress lagi.

**Rough size comparison:**

| | Access token size |
|---|---|
| Sebelum (permissions di JWT) | ~3.8 KB (untuk user dengan 160 permissions) |
| Sesudah | ~550 bytes |

### Rekomendasi state management FE

```
┌─────────────┐
│   Signin    │──┐
└─────────────┘  │
                 ├──► permissions state (Pinia/Redux/Zustand)
┌─────────────┐  │
│Switch Company──┤
└─────────────┘  │
                 │
┌─────────────┐  │
│  GET /me    │──┘  (dipanggil saat reload atau setelah 403)
└─────────────┘
```

Jangan simpan permissions di `localStorage` / `sessionStorage` — sensitif dan bisa stale. Cukup state in-memory + panggil `/me` saat reload.

---

## Firebase setup (untuk operator / FE lead)

Untuk endpoint `/core/v1/auth/google` bekerja, ada konfigurasi di tiga sisi:

### Sisi Firebase Console

1. Buat / pakai existing Firebase project.
2. **Authentication → Sign-in method → Google** → enable.
3. Set **Authorized domains** untuk semua domain FE (mis. `app.tuai.id`, `localhost` untuk dev).
4. **Project Settings → Service accounts → Generate new private key** → unduh JSON. Ini kredensial untuk BE.

### Sisi Backend (env)

```bash
# Wajib — id project Firebase
FIREBASE_PROJECT_ID=tuai-prod

# Kredensial service account (raw JSON content, single-line).
# Kosongkan kalau pakai workload identity / ADC di GCP.
FIREBASE_CREDENTIALS_JSON={"type":"service_account","project_id":"tuai-prod",...}
```

> Kalau `FIREBASE_PROJECT_ID` kosong, BE tetap boot tapi endpoint `/auth/google` akan return `503 Google sign-in is not configured`. Log saat startup: `FIREBASE_PROJECT_ID not set — /auth/google endpoint disabled`.

### Sisi Frontend

```js
import { initializeApp } from 'firebase/app'
import { getAuth, GoogleAuthProvider, signInWithPopup } from 'firebase/auth'

const firebaseApp = initializeApp({
  apiKey:        import.meta.env.VITE_FIREBASE_API_KEY,
  authDomain:    import.meta.env.VITE_FIREBASE_AUTH_DOMAIN,
  projectId:     import.meta.env.VITE_FIREBASE_PROJECT_ID, // wajib sama dgn BE
})

async function loginWithGoogle() {
  const auth = getAuth(firebaseApp)
  const result = await signInWithPopup(auth, new GoogleAuthProvider())
  const idToken = await result.user.getIdToken()

  const res = await fetch('/core/v1/auth/google', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id_token: idToken }),
  })
  const json = await res.json()
  // simpan tokens + permissions seperti flow signin biasa
  return json
}
```

> `projectId` di FE harus identik dengan `FIREBASE_PROJECT_ID` di BE — kalau tidak, BE menolak token (audience mismatch).

---

**Last Updated:** 2026-05-16 (Sign in with Google via Firebase)
**Previously:** 2026-04-20 (JWT permissions extraction → Redis cache)
