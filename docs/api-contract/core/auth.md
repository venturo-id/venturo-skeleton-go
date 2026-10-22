# Authentication API Contract

**Version:** v1
**Base URL:** `/core/v1/auth`
**Module:** Core Authentication
**Last Updated:** 2026-10-21

---

## ⚠️ Breaking Change (2026-10-21) — Company / client / branch dihapus

Domain tenancy (clients, companies, company_users, branches, user_branches) sudah dihapus dari backend. Dampak ke FE:

- **Endpoint dihapus:** `POST /auth/switch-company` dan `GET /auth/companies`.
- **Response `/signin`, `/google`, `/me`:** field `company` dan `client` dihapus. `roles` & `permissions` sekarang global (tidak di-scope ke company).
- **JWT:** claim `company_id`, `company_name`, `client_id`, `client_slug` dihapus.
- **`POST /signup`:** field `company_name` dihapus dari request, object `company` dihapus dari response. Tidak ada lagi company / branch yang dibuat otomatis.
- **User baru (signup maupun first-time Google) tidak mendapat role apa pun** → `"roles": []`, `"permissions": []` sampai super_admin meng-assign role.

---

## 🆕 (2026-05-16) — Sign in with Google (Firebase)

Endpoint baru: `POST /core/v1/auth/google` — autentikasi pakai Firebase Google ID token. FE tinggal kirim `id_token` yang didapat dari Firebase Web SDK, BE memverifikasi, lalu salah satu dari:

- **Returning user** (identity Google sudah ter-link) → sign in normal.
- **Returning user via email match** (sudah punya akun email/password dengan email yang sama) → identity Google otomatis di-link ke akun existing, sign in normal.
- **First-time Google user** → BE otomatis bikin user baru + link identity Google, **tanpa role** (sama seperti signup). Response berisi `is_new_user: true` dan HTTP `201 Created`.

Detail lengkap di [section 3 Sign In with Google](#3-sign-in-with-google).

---

## ⚠️ Breaking Change (2026-04-20) — JWT no longer carries `permissions`

Sebelumnya JWT access token berisi array `permissions` yang ~3.8 KB di kasus user dengan banyak permission → sering kena block ingress (HTTP 400 Bad Request).

Mulai sekarang:

- **JWT tidak lagi berisi field `permissions`.** Permissions disimpan di Redis dan di-lookup backend saat setiap request masuk.
- **Response body TETAP return `permissions`** di `/signin` dan `/google` — FE ambil dari sana.
- **`/refresh` TIDAK mengubah permissions** (user sama). FE cukup pakai permissions yang sudah disimpan dari signin sebelumnya.

### Yang perlu FE lakukan

| Skenario lama | Skenario baru |
|---|---|
| FE decode JWT → ambil `claims.permissions` | FE baca `response.data.permissions` dari signin / google |
| FE decode JWT setelah `refresh` | Tidak perlu — permissions tidak berubah saat refresh |

**Cara check permission di FE:** `response.data.permissions.includes("finance.contacts:read")` (tidak berubah — polanya sama, cuma sumbernya beda).

**Apa yang masih bisa di-decode dari JWT:** `user_id`, `email`, `username`, `full_name`, `is_super_admin`, `roles`, `exp`, `iat`.

---

## Overview

Authentication API untuk registrasi, login, dan token management. Menggunakan JWT + refresh token.

**Key Features:**
- JWT-based authentication dengan refresh token rotation
- Multi-device session management
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
  "is_super_admin": false,
  "roles": ["administrator"],
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
| `is_super_admin` | bool | `true` = bypass semua permission check di backend |
| `roles` | string[] | Role codes user (mis. `["administrator"]`). `[]` jika user belum punya role |
| `exp` | int64 | Expiry (Unix timestamp, default 24 jam) |
| `iat` | int64 | Issued-at (Unix timestamp) |
| `nbf` | int64 | Not-before (Unix timestamp) |
| `iss` | string | Selalu `"tuai-api"` |
| `sub` | string | Subject = `user_id` |

> **Catatan:** Jangan bergantung pada urutan field. Field yang tidak kamu pakai boleh diabaikan.

### Permissions (di response body, bukan di JWT)

Permissions sekarang dikirim lewat **response body** pada endpoint berikut:
- `POST /signin` → `data.permissions`
- `POST /google` → `data.permissions`
- `GET /me` → `data.permissions`

Di backend, permissions di-cache di Redis per user (key `perms:user:<user_id>`). User tanpa role mendapat `[]` (tidak pernah `null`).

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
- `admin` → `["create", "read", "update", "delete", "export", "import", "restore"]`

Sehingga response-nya flat: `["dashboard:read", "user-management:create", "user-management:read", ...]`. FE tidak perlu tahu level — cukup pakai flat array.

### Invalidation / kapan permissions berubah

Setelah signin, permissions **tetap valid sampai** salah satu dari:
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
| `roles` | string[] | Role codes user (mis. `["administrator"]`) |
| `permissions` | string[] | **Flat permission list** — FE pakai untuk gating UI |

> User yang belum punya role (mis. baru signup) tetap bisa sign in, dengan `"roles": []` dan `"permissions": []` — tidak pernah `null`.

**Errors:**

| Status | Message | Kapan |
|--------|---------|-------|
| 400 | Invalid request payload | Payload tidak valid |
| 401 | Invalid credentials | Email/password salah |
| 401 | User account is not active | `is_active = false` |
| 401 | User account is locked | Terkunci setelah 5x gagal |

---

### 2. Sign Up

Registrasi user baru. User dibuat **tanpa role** — belum punya akses apa pun sampai super_admin meng-assign role.

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
  "phone": "+6281234567890"
}
```

| Field | Type | Required | Validasi |
|-------|------|----------|----------|
| `email` | string | Ya | Format email valid |
| `username` | string | Ya | 3-100 karakter |
| `password` | string | Ya | Min 8 karakter |
| `full_name` | string | Tidak | Max 255 karakter |
| `phone` | string | Tidak | Max 20 karakter |

**Response (201):**
```json
{
  "data": {
    "message": "User registered successfully.",
    "user": {
      "id": "550e8400-...",
      "email": "newuser@example.com",
      "username": "johndoe",
      "full_name": "John Doe"
    }
  },
  "message": "User registered successfully."
}
```

> **Yang terjadi saat registrasi:**
> 1. User dibuat dengan `is_active=true`, `is_email_verified=true`
> 2. **Tidak ada role yang di-assign** — saat sign in, `roles` dan `permissions` masih `[]` sampai super_admin meng-assign role

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
    "roles": [],
    "permissions": []
  },
  "message": "Account created and signed in with Google"
}
```

#### Response Fields

Sama persis dengan `/signin` ditambah satu field baru:

| Field | Type | Keterangan |
|-------|------|------------|
| `is_new_user` | bool | `true` jika request ini bikin akun baru (user baru, tanpa role). `false` jika user sudah ada (entah identity Google sudah ter-link, atau user existing email/password yang baru saja di-link otomatis ke Google) |
| `access_token` | string | JWT — pakai di header `Authorization: Bearer <token>` |
| `refresh_token` | string | Refresh token (32 byte opaque) — simpan aman |
| `token_type` | string | Selalu `"Bearer"` |
| `expires_in` | int | Lifetime access_token (detik, default 86400) |
| `user` | object | Info user — untuk first-time, `username` di-derive dari email local-part (mis. `john.doe@gmail.com` → `john.doe`), collisions di-resolve dengan suffix random |
| `roles` | string[] | Role codes user. `[]` untuk first-time user |
| `permissions` | string[] | Flat permission list (sama format dengan `/signin`). `[]` untuk first-time user |

#### Status codes

| Status | Kapan |
|--------|-------|
| 200 | Returning user — sign in sukses |
| 201 | First-time Google user — akun baru dibuat + sign in |

#### Skenario internal (FE tidak perlu tahu, tapi berguna untuk debugging)

| Kondisi BE | `is_new_user` | Akibatnya |
|---|---|---|
| `user_identities` row sudah ada untuk (google, firebase_uid) | `false` | Identity di-refresh, user existing sign in |
| Belum ada identity, tapi `core.users.email` sudah match | `false` | Identity Google **di-link otomatis** ke user existing. User existing sign in |
| Belum ada user sama sekali | `true` | BE membuat `core.users` + `core.user_identities` saja. **Tidak ada role yang di-assign** (sama seperti signup) |

> **First-time Google user belum punya role** — `roles` dan `permissions` kosong `[]` sampai super_admin meng-assign role.

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

const { access_token, refresh_token, permissions, is_new_user } = json.data
storeTokens(access_token, refresh_token)
setPermissions(permissions)

if (is_new_user) {
  // optional: tampilkan welcome screen. User baru belum punya role,
  // jadi permissions masih [] sampai super_admin meng-assign role.
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

Revoke satu refresh token.

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

---

### 6. Logout All Devices

Revoke semua refresh token milik user.

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

---

### 7. Get Me (Profile + Effective Permissions)

Ambil profile user yang sedang login beserta **effective permissions**. Permissions dibaca dari Redis (authz cache), bukan dari decode JWT.

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

> User yang belum punya role mendapat `"roles": []` dan `"permissions": []`.

**Kapan FE memanggil endpoint ini:**

1. **Setelah page reload** — untuk re-hydrate permissions state tanpa simpan di localStorage (data sensitif)
2. **Setelah dapet 403 tak terduga** — untuk cek apakah permissions user telah berubah (admin revoke role misal). Kalau `permissions` turun, update state & tampilkan notif. Kalau tetap sama, berarti memang user tidak punya akses.
3. **Periodik (opsional)** — kalau app-mu butuh real-time awareness terhadap perubahan permissions, poll `/me` tiap N menit. Tapi untuk kebanyakan app, cara (1) dan (2) sudah cukup.

**Perbedaan dari signin:**

| | `/signin` | `/me` |
|---|---|---|
| Return tokens? | ✅ ya | ❌ tidak |
| Mengubah server state? | ✅ ya | ❌ read-only |
| Bisa dipanggil berulang? | ❌ (butuh password) | ✅ unlimited |
| Source permissions? | DB + isi cache | Redis cache (fallback DB) |

**Errors:**

| Status | Message | Kapan |
|--------|---------|-------|
| 401 | User not active | User di-deactivate setelah JWT di-issue |
| 401 | Authentication required | Token missing/invalid/expired |

---

## Authentication Flow

```
Email/Password:
  1. POST /auth/signup            → Registrasi user (tanpa role — tunggu super_admin assign role)
  2. POST /auth/signin            → Login, dapat token + roles + permissions

Google (Firebase):
  1. FE: signInWithPopup(GoogleAuthProvider) → idToken = user.getIdToken()
  2. POST /auth/google { id_token } → BE verify + sign in
     - is_new_user=true: BE bikin user baru (tanpa role)
     - is_new_user=false: returning user / existing email auto-linked

Common:
  3. Semua request pakai header: Authorization: Bearer <access_token>
  4. GET  /auth/me                → (Opsional) Rehydrate profile+permissions setelah reload
  5. POST /auth/refresh           → Saat access_token expired, tukar refresh_token
  6. POST /auth/logout            → Logout dari device ini
```

### Token Expiry

| Token | Default Expiry |
|-------|---------------|
| Access token | 24 jam |
| Refresh token | 7 hari |

---

## FE Integration Checklist (migrasi dari JWT-embedded permissions)

Kalau FE kamu sebelumnya pakai permissions dari JWT, berikut langkah migrasinya:

- [ ] Stop decode JWT untuk ambil `permissions`. Hapus semua `jwtDecode(token).permissions`.
- [ ] Simpan `response.data.permissions` dari `/signin` ke state/store FE (Pinia, Redux, dst).
- [ ] Pada `/refresh`, **tidak perlu update** permissions — user sama, permissions tidak berubah.
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

**Last Updated:** 2026-10-21 (Company / client / branch dihapus — switch-company & companies dihapus, user baru tanpa role)
**Previously:** 2026-05-16 (Sign in with Google via Firebase), 2026-04-20 (JWT permissions extraction → Redis cache)
