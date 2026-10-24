# User Management API Contract

**Version:** v1
**Base URL:** `/core/v1/users`
**Module:** Core User Management
**Last Updated:** 2026-10-24

---

## ⚠️ Breaking Change (2026-10-24) — foto profil di-upload, `avatar_url` tidak lagi diterima

- **Endpoint baru [`PUT /users/me/avatar`](#9-upload-avatar)** (multipart, field `file`: JPEG, PNG, atau WebP) dan **[`DELETE /users/me/avatar`](#10-remove-avatar)**. Server mengubah gambar jadi JPEG ≤512 px (metadata seperti EXIF/GPS dibuang) dan menyimpannya di R2; `avatar_url` jadi `<R2_PUBLIC_URL>/avatars/<user_id>/<id>.jpg`.
- **`avatar_url` tidak lagi diterima di `PUT /users/me` dan `PUT /users/:id`.** Field itu **diabaikan tanpa error** (seperti field tak dikenal lainnya) — client lama tetap dapat `200`, tapi avatar tidak berubah. Alasannya: `avatar_url` hanya boleh menunjuk ke origin publik R2 (CSP aplikasi desktop hanya mengizinkan origin itu), jadi URL bebas dari client tidak lagi disimpan. Ganti foto lewat `PUT /users/me/avatar`.
- **Error kedua endpoint ini membawa `code`** di envelope (mis. `"code": "invalid_image"`): FE bercabang pada `code`, bukan pada `message`. Sejak rilis yang sama **semua** error API membawa `code` — termasuk `email_taken` / `username_taken` di Create / Update dan `incorrect_password` / `no_password` / `account_locked` di Change Password (daftar lengkap: [README](../README.md#error-codes)).

---

## ⚠️ Breaking Change (2026-10-23) — email, status, dan akun sendiri tidak lewat `/users/:id`

- **`PUT /users/:id` ke diri sendiri dengan `email` baru → `403 Cannot change your own email here`.** Sebelumnya email langsung berganti (jadi belum terverifikasi) hanya dengan session, padahal role-nya tetap — access token curian cukup untuk memindahkan akun ke alamat lain lalu mengklaimnya. Ganti email sendiri lewat [`POST /account/email`](account.md#3-request-email-change), yang meminta password lagi dan mengonfirmasi alamat baru lewat link. Mengirim email yang sama dengan sekarang tetap boleh.
- **`PUT /users/:id` ke diri sendiri dengan `is_active: false`, dan `DELETE /users/:id` ke diri sendiri → `403 Cannot deactivate or delete your own account`**, termasuk untuk super admin: session saja tidak cukup untuk mematikan akun, dan super admin terakhir tidak bisa menghapus dirinya. Pemilik menutup akunnya lewat [`POST /account/delete`](account.md#9-delete-account).
- **Ganti password (`PUT /users/me/password`) membatalkan link email yang belum dipakai**, termasuk link [ganti email](account.md#3-request-email-change).
- **Update tidak lagi menimpa perubahan yang terjadi bersamaan.** Kalau user yang sama sedang dinonaktifkan admin lain, mengganti email, atau mereset password, request menunggu perubahan itu selesai lalu menerapkan field yang dikirim di atasnya — bukan menulis ulang data yang dibacanya sebelum itu.

---

## ⚠️ Behaviour Change (2026-10-23) — timezone, self-service akun, audit log

- **Field baru `timezone`** di user object (selalu dikirim): nama zona IANA, mis. `Asia/Jakarta`, `America/New_York`, `UTC`. Default `Asia/Jakarta`, juga untuk user yang sudah ada. Opsional di `POST /users`, `PUT /users/:id`, dan `PUT /users/me`. Zona tidak dikenal, `Local`, atau string kosong → `400` dengan `errors.timezone: ["is invalid (timezone)"]`. Timestamp tetap UTC; zona hanya menentukan cara tanggal ditampilkan dan dikelompokkan. Lihat [Timezone](auth.md#timezone).
- **Self-service akun ada di [account.md](account.md):** user mengganti email sendiri (`POST /account/email`, dikonfirmasi lewat link ke alamat baru), melihat / mengakhiri sesi, mengekspor datanya, dan menghapus akunnya sendiri. Akun yang dihapus pemiliknya hilang dari `GET /users` seperti user yang dihapus admin.
- **Create, update, dan delete user dicatat di [audit log](audit-log.md)** (`user.created`, `user.updated` dengan nama field yang diubah, `user.deleted`) — tanpa nilai field-nya.

---

## ⚠️ Breaking / Behaviour Change (2026-10-23) — role hanya untuk email terverifikasi, ganti password ikut lockout

- **`PUT /users/:id` dengan `role_id` untuk user yang emailnya belum terverifikasi → `409 The user's email is not verified`.** Siapa pun bisa signup dengan alamat orang lain, jadi role hanya diberikan ke alamat yang sudah terbukti. Minta user memverifikasi emailnya, atau — kalau kamu bisa menjamin alamatnya — kirim `is_email_verified: true` di request yang sama.
- **Field baru `is_email_verified`** di `PUT /users/:id`: admin menjamin (atau mencabut) verifikasi email user lain. Untuk diri sendiri → `403 Cannot change the verification of your own email`.
- **Ganti password mengirim email notifikasi** ke pemilik akun.
- **`current_password` yang salah di `PUT /users/me/password` ikut lockout sign-in.** Setelah `MAX_LOGIN_ATTEMPTS` kali salah → `429 Too many incorrect passwords` (sign-in juga terkunci, pemilik dikirimi email). Akun Google tanpa password → `409 This account has no password yet` (sebelumnya `400 Current password is incorrect`). `404 User not found` diganti `401 User account is not active`. Lihat [Change Password](#8-change-password).

---

## ⚠️ Breaking Change (2026-10-23) — Timebox baseline

- **Permission `user-management:*` jadi `users:*`** (`users:read`, `users:create`, `users:update`, `users:delete`).
- **Kebijakan password** di Create dan Change Password: 8–72 byte, huruf besar + huruf kecil + angka, bukan password umum → selain itu `400` dengan error per field.
- **Ganti password mengakhiri semua sesi, termasuk yang sedang dipakai:** semua refresh token di-revoke dan access token yang sudah terbit ditolak (`401 Session has been revoked. Please sign in again.`). Message sukses jadi `Password changed successfully. Please sign in again.`; password lama salah → `400 Current password is incorrect` (sebelumnya `Invalid current password`).
- **Nonaktif / hapus user** sekarang juga mematikan access token user itu seketika (selain refresh token dan API key-nya).
- **`PUT /users/me` menolak API key** (`403`), sama seperti `PUT /users/me/password`.
- **`:id` yang bukan UUID** → `400 Invalid id` (detail `must be a UUID`), menggantikan `User ID is required`.

---

## Overview

API untuk mengelola akun user: CRUD oleh admin dan self-service profil.

**Key Features:**
- User CRUD dengan soft delete
- Self-service profil (`/me`), foto profil (upload ke R2), dan ganti password
- **Role assignment** inline lewat `role_id` di Create/Update (global, tersimpan di `core.user_roles`) — hanya untuk user yang emailnya terverifikasi
- RBAC — read/create/update/delete butuh permission `users:*`
- Batas atas (ceiling): caller hanya bisa memberi role, atau mengelola user, yang permission-nya ia pegang sendiri
- Tidak ada tenant scope — pemegang `users:read` melihat semua user yang belum dihapus

---

## User Object

```json
{
  "id": "550e8400-...",
  "email": "rina@timebox.id",
  "username": "rina",
  "full_name": "Rina Wijaya",
  "phone": "+6281234567890",
  "timezone": "Asia/Jakarta",
  "is_active": true,
  "is_email_verified": true,
  "role_name": "User Manager",
  "last_login_at": "2026-10-23T02:15:00Z",
  "created_at": "2026-10-20T08:00:00Z",
  "updated_at": "2026-10-23T02:15:00Z"
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `id` | UUID | ID user |
| `email` | string | Email (unik, lowercase) |
| `username` | string | Username (unik, lowercase) |
| `full_name` | string | Nama lengkap — tidak dikirim kalau kosong |
| `phone` | string | Nomor telepon — tidak dikirim kalau kosong |
| `avatar_url` | string | URL foto profil, `<R2_PUBLIC_URL>/avatars/<user_id>/<id>.jpg`: JPEG ≤512 px. Tiap upload mendapat URL baru, jadi isinya tidak pernah berubah dan boleh di-cache selamanya. Avatar dari sebelum upload ada bisa berupa URL lain (mis. foto Google). Hanya berubah lewat [`PUT /users/me/avatar`](#9-upload-avatar) / [`DELETE /users/me/avatar`](#10-remove-avatar). Tidak dikirim kalau kosong |
| `timezone` | string | Zona IANA untuk menampilkan dan mengelompokkan tanggal user ini (default `Asia/Jakarta`). Timestamp tetap UTC |
| `is_active` | bool | Status aktif |
| `is_email_verified` | bool | Kepemilikan email sudah terbukti: dibuat atau dijamin admin, di-seed, dari Google, kode verifikasi, atau reset password. `false` (mis. hasil signup) → belum bisa diberi role, dan akunnya bisa [diklaim](auth.md#klaim-akun-email-belum-terverifikasi) pemilik asli email itu |
| `role_name` | string | **Nama** (bukan kode) role pertama yang di-assign ke user, untuk tampilan. Tidak dikirim kalau user belum punya role |
| `last_login_at` | timestamp | Sign in terakhir — tidak dikirim kalau belum pernah |
| `created_at` | timestamp | Waktu dibuat (UTC) |
| `updated_at` | timestamp | Waktu terakhir diubah (UTC) |

> Field opsional yang kosong **tidak dikirim** (bukan `null`). `role_name` berasal dari join `core.user_roles` + `core.roles`, tidak tersimpan di `core.users`.

---

## Admin Endpoints

### 1. List All Users

```
GET /core/v1/users
```

**Auth:** Bearer token atau API key
**Permission:** `users:read`

**Query Parameters:**

| Param | Type | Default | Keterangan |
|-------|------|---------|------------|
| `page` | int | 1 | 1 – 100000 |
| `limit` | int | 10 | 1 – 100 |
| `search` | string | - | Max 255. Cocok sebagian (tidak case-sensitive) di email, username, atau full_name |
| `is_active` | bool | - | Filter status aktif |

Urutan: terbaru dulu (`created_at` turun).

**Response (200):**
```json
{
  "data": [
    {
      "id": "550e8400-...",
      "email": "rina@timebox.id",
      "username": "rina",
      "full_name": "Rina Wijaya",
      "timezone": "Asia/Jakarta",
      "is_active": true,
      "is_email_verified": true,
      "role_name": "User Manager",
      "last_login_at": "2026-10-23T02:15:00Z",
      "created_at": "2026-10-20T08:00:00Z",
      "updated_at": "2026-10-23T02:15:00Z"
    }
  ],
  "message": "Users retrieved successfully",
  "meta": {
    "pagination": {
      "page": 1,
      "limit": 10,
      "total": 7,
      "total_pages": 1
    }
  }
}
```

**Errors:**

| Status | `code` | Message |
|--------|--------|---------|
| 400 | `validation_failed` | Invalid query parameters |
| 403 | `forbidden` | Forbidden — `Insufficient permissions` (tidak punya `users:read`) |

---

### 2. Get User by ID

```
GET /core/v1/users/:id
```

**Auth:** Bearer token atau API key
**Permission:** `users:read`

**Response (200):** User object, message `User retrieved successfully`.

**Errors:**

| Status | `code` | Message |
|--------|--------|---------|
| 400 | `bad_request` | Invalid id (`:id` bukan UUID) |
| 403 | `forbidden` | Forbidden — `Insufficient permissions` |
| 404 | `not_found` | User not found (tidak ada atau sudah dihapus) |

---

### 3. Create User

```
POST /core/v1/users
```

**Auth:** Bearer token atau API key
**Permission:** `users:create`

**Request:**
```json
{
  "email": "budi@timebox.id",
  "username": "budi",
  "password": "Jadwal-Tetap-9",
  "full_name": "Budi Santoso",
  "phone": "+6281298765432",
  "timezone": "Asia/Makassar",
  "role_id": "00000000-0000-0000-0000-000000000002"
}
```

| Field | Type | Required | Validasi |
|-------|------|----------|----------|
| `email` | string | Ya | Format email, max 254. Disimpan lowercase |
| `username` | string | Ya | 3–50 karakter: huruf, angka, `.`, `_`, `-`; diawali huruf/angka. Disimpan lowercase |
| `password` | string | Ya | 8–72 byte, minimal satu huruf besar, satu huruf kecil, dan satu angka; bukan password umum |
| `full_name` | string | Tidak | Max 255 karakter |
| `phone` | string | Tidak | Max 20 karakter |
| `timezone` | string | Tidak | Nama zona IANA, max 64. Tidak dikirim / `null` → `Asia/Jakarta`. Lihat [Timezone](auth.md#timezone) |
| `role_id` | UUID | Tidak | Role yang di-assign. Omit = user dibuat **tanpa role** (belum punya akses apa pun) |

> User dibuat dengan `is_active: true` dan `is_email_verified: true` — admin yang membuat akun menjamin emailnya. Akibatnya, kalau pemilik email itu sign in lewat `POST /auth/google`, identity Google-nya langsung di-link ke akun ini. Pastikan emailnya benar.
>
> Insert user dan assignment role dijalankan dalam **satu transaksi** — kalau assignment gagal, user ikut batal dibuat.

**Validasi `role_id`:**

- Role tidak ada / sudah dihapus → `400 Invalid role_id`.
- Role `super_admin` hanya boleh di-assign oleh super admin → selain itu `403 Role not allowed for caller`.
- **Ceiling:** role tidak boleh memberi permission yang tidak dipegang caller → `403 Role not allowed for caller`. Mis. caller dengan `users: editor` tidak bisa meng-assign `administrator` (`users: admin`, `roles: admin`).

**Response (201):** User object yang baru dibuat, message `User created successfully`.

**Errors:**

| Status | `code` | Message | Keterangan |
|--------|--------|---------|------------|
| 400 | `validation_failed` | Invalid request payload | Validasi gagal (`errors` per field) |
| 400 | `bad_request` | Invalid role_id | `role_id` tidak ada / sudah dihapus |
| 403 | `forbidden` | Role not allowed for caller | `super_admin` dari non-super-admin, atau role dengan permission yang tidak dipegang caller |
| 409 | `email_taken` | Email already exists | Dipakai user lain yang belum dihapus |
| 409 | `username_taken` | Username already exists | Dipakai user lain yang belum dihapus |

---

### 4. Update User

```
PUT /core/v1/users/:id
```

**Auth:** Bearer token atau API key
**Permission:** `users:update`

**Request:** (semua field opsional; field yang tidak dikirim atau `null` tidak diubah)
```json
{
  "email": "budi.santoso@timebox.id",
  "username": "budi.santoso",
  "full_name": "Budi Santoso",
  "phone": "+6281298765432",
  "timezone": "Asia/Makassar",
  "is_active": false,
  "is_email_verified": true,
  "role_id": "7d1f5c2a-..."
}
```

| Field | Type | Validasi / Keterangan |
|-------|------|------------|
| `email` | string | Format email, max 254, unik, disimpan lowercase |
| `username` | string | Aturan sama dengan Create, unik, disimpan lowercase |
| `full_name` | string | Max 255 karakter |
| `phone` | string | Max 20 karakter |
| `timezone` | string | Nama zona IANA, max 64. `Local` / tidak dikenal / `""` → `400` |
| `is_active` | bool | Aktif / nonaktif |
| `is_email_verified` | bool | `true` = admin menjamin email user ini; `false` = mencabut verifikasinya. Tidak untuk diri sendiri |
| `role_id` | UUID | Kalau dikirim: **mengganti semua** role user dengan role ini. User harus sudah (atau di request ini) terverifikasi emailnya. Tidak ada cara mengosongkan role lewat API |

> Password **tidak bisa** diubah dari endpoint ini — user menggantinya sendiri lewat Change Password.
>
> Foto profil juga **tidak bisa** diubah di sini: `avatar_url` di body diabaikan (tanpa error). Pemiliknya mengganti lewat [`PUT /users/me/avatar`](#9-upload-avatar).
>
> **Nonaktifkan (`is_active: true → false`):** semua sesi user diakhiri — refresh token di-revoke, access token-nya langsung `401`, dan API key-nya ditolak (`401 User account is not active`) selama nonaktif. Setelah diaktifkan lagi user harus sign in ulang.
>
> Ganti role atau status aktif berlaku di request berikutnya user itu, walau tokennya sama.
>
> Mengubah email user lain membuatnya `is_email_verified: true` (admin menjamin, sama seperti Create). Email **sendiri** tidak bisa diubah di sini → `403`; pakai [`POST /account/email`](account.md#3-request-email-change).
>
> **Role hanya untuk email terverifikasi:** `role_id` untuk user yang `is_email_verified`-nya `false` (setelah perubahan di request ini) → `409`. `is_email_verified: true` dan `role_id` boleh dikirim bersamaan. Jaminlah hanya alamat yang kamu yakini milik orang itu: akun hasil signup bisa didaftarkan orang lain dengan alamat siapa pun.
>
> `is_email_verified: false` tidak mencabut role yang sudah dipegang dan tidak mengakhiri sesi, tapi membuat akun itu bisa [diklaim](auth.md#klaim-akun-email-belum-terverifikasi) lagi lewat Google sign-in atau reset password.

**Proteksi:**

- `role_id` divalidasi sama seperti di Create (lihat "Validasi `role_id`").
- Tidak bisa mengganti role sendiri (`:id` = caller, `role_id` dikirim) → `403 Cannot change your own role` (berlaku juga untuk super admin).
- Tidak bisa mengubah `is_email_verified` sendiri (`:id` = caller, field dikirim) → `403 Cannot change the verification of your own email`.
- Tidak bisa mengganti email sendiri (`:id` = caller, `email` berbeda dari sekarang) → `403 Cannot change your own email here` (detail `Use POST /core/v1/account/email, which confirms the new address`).
- Tidak bisa menonaktifkan diri sendiri (`:id` = caller, `is_active: false`) → `403 Cannot deactivate or delete your own account` (detail `Use POST /core/v1/account/delete to close your account`).
- `role_id` untuk user yang emailnya belum terverifikasi → `409 The user's email is not verified`.
- Non-super-admin tidak bisa mengubah user pemegang role `super_admin` → `403 Cannot manage a super admin user`.
- Tidak bisa mengubah user yang memegang permission di luar milik caller → `403 Cannot manage a user with permissions you do not hold`.

**Response (200):** User object terbaru, message `User updated successfully`.

**Errors:**

| Status | `code` | Message | Keterangan |
|--------|--------|---------|------------|
| 400 | `bad_request` | Invalid id | `:id` bukan UUID |
| 400 | `validation_failed` | Invalid request payload | Validasi gagal (`errors` per field) |
| 400 | `bad_request` | Invalid role_id | `role_id` tidak ada / sudah dihapus |
| 403 | `forbidden` | Cannot change your own role | `role_id` dikirim untuk diri sendiri |
| 403 | `forbidden` | Role not allowed for caller | Lihat validasi `role_id` |
| 403 | `forbidden` | Cannot manage a super admin user | Non-super-admin mengubah pemegang `super_admin` |
| 403 | `forbidden` | Cannot manage a user with permissions you do not hold | Target memegang permission di luar milik caller |
| 403 | `forbidden` | Cannot change the verification of your own email | `is_email_verified` dikirim untuk diri sendiri |
| 403 | `forbidden` | Cannot change your own email here | `email` baru dikirim untuk diri sendiri |
| 403 | `forbidden` | Cannot deactivate or delete your own account | `is_active: false` dikirim untuk diri sendiri |
| 404 | `not_found` | User not found | Tidak ada atau sudah dihapus |
| 409 | `email_taken` | Email already exists | - |
| 409 | `username_taken` | Username already exists | - |
| 409 | `conflict` | The user's email is not verified — detail `Ask the user to verify their email, or set is_email_verified to true if you can vouch for the address` | `role_id` dikirim untuk user yang emailnya belum terverifikasi |

---

### 5. Delete User

```
DELETE /core/v1/users/:id
```

**Auth:** Bearer token atau API key
**Permission:** `users:delete`

> Soft delete: email dan username bisa dipakai lagi oleh akun baru. Setelah dihapus, semua sesi user diakhiri (refresh token di-revoke, access token dan API key-nya langsung ditolak `401`).
>
> Proteksi target sama dengan Update: non-super-admin tidak bisa menghapus pemegang `super_admin`, dan tidak ada yang bisa menghapus user yang memegang permission di luar milik caller.
>
> Menghapus diri sendiri (`:id` = caller) → `403 Cannot deactivate or delete your own account`. User yang ingin menghapus akunnya sendiri (data pribadinya ikut dihapus): [`POST /account/delete`](account.md#9-delete-account).

**Response (200):**
```json
{
  "data": null,
  "message": "User deleted successfully"
}
```

**Errors:**

| Status | `code` | Message |
|--------|--------|---------|
| 400 | `bad_request` | Invalid id |
| 403 | `forbidden` | Cannot deactivate or delete your own account |
| 403 | `forbidden` | Cannot manage a super admin user |
| 403 | `forbidden` | Cannot manage a user with permissions you do not hold |
| 404 | `not_found` | User not found (tidak ada atau sudah dihapus) |

---

## Self-Service Endpoints

Untuk user mengelola akunnya sendiri — tidak butuh permission, cukup login.

### 6. Get Current User (Me)

```
GET /core/v1/users/me
```

**Auth:** Bearer token **atau API key** (API key → profil pemilik key)

**Response (200):** User object milik caller, message `User retrieved successfully`.

> Untuk roles + permissions efektif, pakai [`GET /auth/me`](auth.md#8-get-me-profile--effective-permissions).

---

### 7. Update Profile (Me)

```
PUT /core/v1/users/me
```

**Auth:** Bearer token (API key → `403`)

**Request:** (semua opsional; tidak dikirim atau `null` = tidak diubah)
```json
{
  "full_name": "Rina Wijaya",
  "phone": "+6281234567890",
  "timezone": "Asia/Jakarta"
}
```

| Field | Type | Validasi |
|-------|------|----------|
| `full_name` | string | Max 255 karakter |
| `phone` | string | Max 20 karakter |
| `timezone` | string | Nama zona IANA, max 64 (mis. dari `Intl.DateTimeFormat().resolvedOptions().timeZone`). `Local` / tidak dikenal / `""` → `400` dengan `errors.timezone` |

> Email, username, dan status aktif **tidak bisa** diubah dari endpoint ini. Ganti email sendiri: [`POST /account/email`](account.md#3-request-email-change).
>
> `avatar_url` **tidak lagi diterima** dan diabaikan tanpa error. Ganti foto lewat [`PUT /users/me/avatar`](#9-upload-avatar), hapus lewat [`DELETE /users/me/avatar`](#10-remove-avatar).

**Response (200):** User object terbaru, message `User updated successfully`.

**Errors:**

| Status | `code` | Message |
|--------|--------|---------|
| 400 | `validation_failed` | Invalid request payload |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` |

---

### 8. Change Password

```
PUT /core/v1/users/me/password
```

**Auth:** Bearer token (API key → `403`)

**Request:**
```json
{
  "current_password": "Kalender-Rapi-7",
  "new_password": "Kalender-Baru-8"
}
```

| Field | Type | Required | Validasi |
|-------|------|----------|----------|
| `current_password` | string | Ya | Password saat ini, max 72 karakter |
| `new_password` | string | Ya | 8–72 byte, minimal satu huruf besar, satu huruf kecil, dan satu angka; bukan password umum |

**Response (200):**
```json
{
  "data": null,
  "message": "Password changed successfully. Please sign in again."
}
```

> Setelah password diganti, **semua sesi diakhiri, termasuk sesi ini**: semua refresh token di-revoke dan access token yang sudah terbit → `401 session_ended` (`Session has been revoked. Please sign in again.`). FE: arahkan user ke halaman login.
>
> Pemilik akun dikirimi email bahwa password-nya baru diganti. Kode email yang belum dipakai — termasuk kode [ganti email](account.md#3-request-email-change) — ikut batal.
>
> **`current_password` yang salah dihitung ke lockout yang sama dengan sign-in**, supaya access token curian tidak bisa dipakai menebak password tanpa batas. Setelah `MAX_LOGIN_ATTEMPTS` kali salah, akun terkunci selama `ACCOUNT_LOCKOUT_DURATION`: pemilik dikirimi email, sign-in ditolak, dan endpoint ini menjawab `429` untuk password benar maupun salah. Batas ini juga berlaku untuk tebakan yang dikirim paralel: percobaan dihitung sebelum password dicek, jadi tidak lebih dari `MAX_LOGIN_ATTEMPTS` tebakan yang sempat dicek.
>
> User yang dibuat lewat Google belum punya password lokal → `409 This account has no password yet`. Buat password lewat [`POST /auth/forgot-password`](auth.md#11-forgot-password); lupa password juga lewat jalur itu.

**Errors:**

| Status | `code` | Message |
|--------|--------|---------|
| 400 | `validation_failed` | Invalid request payload (mis. `new_password` tidak memenuhi kebijakan) |
| 400 | `incorrect_password` | Current password is incorrect |
| 401 | `user_inactive` | User account is not active |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` |
| 409 | `no_password` | This account has no password yet — `errors.detail`: `Set one through forgot password` |
| 429 | `account_locked` | Too many incorrect passwords — `errors.detail`: `Try again later, or reset your password`. Tanpa `retry_after` / header `Retry-After` |

---

### 9. Upload Avatar

```
PUT /core/v1/users/me/avatar
```

**Auth:** Bearer token (API key → `403`)

**Request:** `multipart/form-data` dengan satu file di field `file`.

```bash
curl -X PUT http://localhost:8080/core/v1/users/me/avatar \
  -H "Authorization: Bearer YOUR_ACCESS_TOKEN" \
  -F "file=@avatar.png"
```

| Field | Type | Required | Validasi |
|-------|------|----------|----------|
| `file` | file | Ya | JPEG, PNG, atau WebP (dikenali dari isinya, bukan nama file / `Content-Type`). Lebar dan tinggi 1–4096 px. Seluruh body maks 2 MiB |

**Yang dilakukan server:**

1. Gambar di-decode; sisi terpanjang di atas 512 px diperkecil ke 512 px (rasio tetap). FE sebaiknya sudah crop persegi ≤512 px, jadi biasanya tidak ada resize.
2. Bagian transparan (PNG/WebP) jadi putih.
3. Di-encode ulang sebagai JPEG (quality 88). Semua metadata — EXIF, lokasi GPS, ICC — terbuang. Orientasi EXIF **tidak** diterapkan: kirim piksel yang sudah tegak (mis. hasil `canvas`).
4. Disimpan di R2 dengan key baru; `avatar_url` jadi `<R2_PUBLIC_URL>/avatars/<user_id>/<id>.jpg`.
5. Foto lama dihapus dari R2 (kalau foto itu ada di R2; URL lain, mis. foto Google, dibiarkan).

**Response (200):** User object terbaru — sama dengan `GET /users/me` — dengan `avatar_url` baru, message `Avatar updated successfully`.

```json
{
  "data": {
    "id": "550e8400-...",
    "email": "rina@timebox.id",
    "username": "rina",
    "full_name": "Rina Wijaya",
    "avatar_url": "https://files.timebox.id/avatars/550e8400-.../3f2b9c1e-....jpg",
    "timezone": "Asia/Jakarta",
    "is_active": true,
    "is_email_verified": true,
    "created_at": "2026-10-20T08:00:00Z",
    "updated_at": "2026-10-24T03:10:00Z"
  },
  "message": "Avatar updated successfully"
}
```

> URL berganti di setiap upload dan isinya tidak pernah berubah — aman di-cache selamanya. Dicatat di [audit log](audit-log.md) sebagai `user.avatar_changed` (tanpa URL-nya).

**Errors:** (semua membawa `code` di envelope)

| Status | `code` | Message | Keterangan |
|--------|--------|---------|------------|
| 400 | `validation_failed` | Invalid request payload — `errors.file`: `["is required"]` | Field `file` tidak ada, atau body bukan `multipart/form-data` |
| 400 | `bad_request` | Invalid request payload — `errors.detail`: `Body or parameters could not be parsed` | Body multipart rusak |
| 400 | `invalid_image` | The file is not a supported image — `errors.detail`: `Upload a JPEG, PNG or WebP image up to 4096×4096` | Bukan JPEG/PNG/WebP (mis. GIF, SVG, HEIC), file rusak, atau lebar/tinggi di luar 1–4096 px |
| 403 | `forbidden` | Forbidden — `errors.detail`: `This endpoint cannot be used with an API key` | Dipanggil dengan API key |
| 404 | `not_found` | User not found | Akun dihapus saat upload berjalan |
| 413 | `payload_too_large` | Request body too large — `errors.detail`: `limit is 2097152 bytes` | Body > 2 MiB |
| 503 | `storage_not_configured` | Avatar storage is not configured | Server ini tidak punya R2 (atau `R2_PUBLIC_URL`) — foto profil tidak bisa diubah |

```json
{
  "data": null,
  "message": "The file is not a supported image",
  "code": "invalid_image",
  "errors": { "detail": ["Upload a JPEG, PNG or WebP image up to 4096×4096"] }
}
```

---

### 10. Remove Avatar

```
DELETE /core/v1/users/me/avatar
```

**Auth:** Bearer token (API key → `403`)

Tanpa body. `avatar_url` dikosongkan dan fotonya dihapus dari R2 (kalau ada di R2). User tanpa avatar tetap dapat `200`.

**Response (200):** User object terbaru tanpa `avatar_url`, message `Avatar removed successfully`.

> Dicatat di [audit log](audit-log.md) sebagai `user.avatar_removed` (hanya kalau memang ada avatar yang dihapus).

**Errors:** (semua membawa `code` di envelope)

| Status | `code` | Message | Keterangan |
|--------|--------|---------|------------|
| 403 | `forbidden` | Forbidden — `errors.detail`: `This endpoint cannot be used with an API key` | Dipanggil dengan API key |
| 404 | `not_found` | User not found | Akun dihapus saat request berjalan |
| 503 | `storage_not_configured` | Avatar storage is not configured | Server ini tidak punya R2 (atau `R2_PUBLIC_URL`) |

---

## Permission Requirements Summary

| Endpoint | Permission | API key | Catatan |
|----------|-----------|---|---|
| `GET /users` | `users:read` | ✅ | - |
| `GET /users/:id` | `users:read` | ✅ | - |
| `POST /users` | `users:create` | ✅ | Satu transaksi; `role_id` dibatasi ceiling |
| `PUT /users/:id` | `users:update` | ✅ | `role_id` mengganti semua role (ceiling, bukan untuk diri sendiri, hanya untuk email terverifikasi); email dan status sendiri tidak bisa diubah; target yang lebih tinggi diproteksi |
| `DELETE /users/:id` | `users:delete` | ✅ | Bukan untuk diri sendiri; target yang lebih tinggi diproteksi |
| `GET /users/me` | Login saja | ✅ | - |
| `PUT /users/me` | Login saja | ❌ | - |
| `PUT /users/me/password` | Login saja | ❌ | Password salah ikut lockout sign-in; mengakhiri semua sesi, mengirim email notifikasi |
| `PUT /users/me/avatar` | Login saja | ❌ | Multipart `file` (JPEG/PNG/WebP, ≤4096 px, body ≤2 MiB) → JPEG ≤512 px di R2; `503` tanpa R2 |
| `DELETE /users/me/avatar` | Login saja | ❌ | `503` tanpa R2 |

> Pemegang role `super_admin` aktif melewati semua cek permission (dibaca dari role saat request, bukan dari claim JWT). API key tidak pernah bertindak sebagai super admin — permission-nya = scope key ∩ permission pemiliknya saat itu.
>
> "Ceiling" = role yang di-assign tidak boleh memberi permission yang tidak dipegang caller sendiri. "Target yang lebih tinggi diproteksi" = PUT/DELETE user pemegang `super_admin` oleh non-super-admin → `403 Cannot manage a super admin user`; PUT/DELETE user yang memegang permission di luar milik caller → `403 Cannot manage a user with permissions you do not hold`.

---

**Last Updated:** 2026-10-24 (upload / hapus foto profil lewat `PUT` / `DELETE /users/me/avatar`; `avatar_url` diabaikan di `PUT /users/me` dan `PUT /users/:id`; error avatar membawa `code`; semua error membawa `code` — `email_taken` / `username_taken`, `incorrect_password` / `no_password` / `account_locked`)
**Previously:** 2026-10-23 (email, status, dan akun sendiri tidak lewat `/users/:id` → `403`; update tidak menimpa perubahan bersamaan), 2026-10-23 (field `timezone` di user object, `POST /users`, `PUT /users/:id`, `PUT /users/me`; self-service ganti email / hapus akun di account.md; create / update / delete user dicatat di audit log), 2026-10-23 (role hanya untuk email terverifikasi → `409`, field `is_email_verified` di `PUT /users/:id`, ganti password mengirim email dan ikut lockout sign-in), 2026-10-23 (Timebox baseline: permission `users:*`, kebijakan password, ganti password mengakhiri semua sesi, nonaktif/hapus mematikan access token, `PUT /users/me` menolak API key, `400 Invalid id`), 2026-10-22 (ceiling role assignment, proteksi target, email & username lowercase)
