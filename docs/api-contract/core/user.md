# User Management API Contract

**Version:** v1
**Base URL:** `/core/v1/users`
**Module:** Core User Management
**Last Updated:** 2026-10-21

---

## Overview

API untuk mengelola user accounts. Termasuk admin CRUD dan self-service profile management.

**Key Features:**
- User CRUD dengan soft delete
- Self-service profile (me endpoints)
- Password change
- **Role assignment** — inline via `role_id` di Create/Update (global, tersimpan di `core.user_roles`)
- RBAC — read/create/update/delete butuh permission `user-management`
- Tidak ada tenant scope — setiap caller dengan `user-management:read` melihat semua user (yang belum dihapus)

---

## User Object

```json
{
  "id": "550e8400-...",
  "email": "admin@tuai.com",
  "username": "admin",
  "full_name": "Admin Tuai",
  "phone": "+6281234567890",
  "avatar_url": null,
  "is_active": true,
  "is_email_verified": false,
  "role_name": "administrator",
  "last_login_at": "2026-04-01T10:00:00Z",
  "created_at": "2026-01-01T08:00:00Z",
  "updated_at": "2026-04-01T10:00:00Z"
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `id` | UUID | ID user |
| `email` | string | Email (unique) |
| `username` | string | Username (unique) |
| `full_name` | string / null | Nama lengkap |
| `phone` | string / null | Nomor telepon |
| `avatar_url` | string / null | URL foto profil |
| `is_active` | bool | Status aktif |
| `is_email_verified` | bool | Email sudah diverifikasi |
| `role_name` | string / null | Nama role pertama user dari `core.user_roles` (untuk display di UI). Absent kalau user belum punya role. |
| `last_login_at` | timestamp / null | Login terakhir |
| `created_at` | timestamp | Waktu dibuat |
| `updated_at` | timestamp | Waktu terakhir diubah |

> `role_name` di-populate otomatis oleh service (tidak tersimpan di tabel `core.users` — berasal dari join ke `core.user_roles` + `core.roles`).

---

## Admin Endpoints

### 1. List All Users

```
GET /core/v1/users
```

**Auth:** Bearer token
**Permission:** `user-management:read`

**Query Parameters:**

| Param | Type | Default | Keterangan |
|-------|------|---------|------------|
| `page` | int | 1 | Halaman |
| `limit` | int | 10 | Max 100 |
| `search` | string | - | Cari di email, username, full_name |
| `is_active` | bool | - | Filter status aktif |

**Response (200):**
```json
{
  "data": [
    {
      "id": "550e8400-...",
      "email": "admin@tuai.com",
      "username": "admin",
      "full_name": "Admin Tuai",
      "phone": null,
      "avatar_url": null,
      "is_active": true,
      "is_email_verified": false,
      "role_name": "administrator",
      "last_login_at": "2026-04-01T10:00:00Z",
      "created_at": "2026-01-01T08:00:00Z",
      "updated_at": "2026-01-01T08:00:00Z"
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

| Status | Message | Error code |
|--------|---------|------------|
| 400 | Invalid query parameters | - |
| 403 | Forbidden (tidak punya `user-management:read`) | - |

---

### 2. Get User by ID

```
GET /core/v1/users/:id
```

**Auth:** Bearer token
**Permission:** `user-management:read`

**Response (200):** Single user object (lengkap dengan `role_name`).

**Errors:**

| Status | Message | Error code |
|--------|---------|------------|
| 400 | User ID is required | - |
| 403 | Forbidden (tidak punya `user-management:read`) | - |
| 404 | User not found (tidak ada atau sudah di-soft-delete) | - |

---

### 3. Create User

```
POST /core/v1/users
```

**Auth:** Bearer token
**Permission:** `user-management:create`

**Request:**
```json
{
  "email": "newuser@tuai.com",
  "username": "newuser",
  "password": "password123",
  "full_name": "New User",
  "phone": "+6281234567890",
  "role_id": "00000000-0000-0000-0000-000000000003"
}
```

| Field | Type | Required | Validasi |
|-------|------|----------|----------|
| `email` | string | Ya | Format email valid |
| `username` | string | Ya | 3-100 karakter |
| `password` | string | Ya | Min 8 karakter |
| `full_name` | string | Tidak | Max 255 karakter |
| `phone` | string | Tidak | Max 20 karakter |
| `role_id` | UUID | Tidak | Role yang di-assign ke user. Omit = user dibuat **tanpa role** (belum punya akses apa pun). |

> User dibuat dengan `is_active: true`, `is_email_verified: false`.
> Insert ke `core.users` dan assignment role ke `core.user_roles` (kalau `role_id` dikirim) dijalankan dalam **satu transaction** — kalau assignment gagal, insert user ikut rollback.

**Validasi `role_id`:**

- Role tidak ada / soft-deleted → `400 Invalid role_id`.
- Non-super-admin tidak boleh meng-assign role `super_admin` → `403 Role not allowed for caller`. Super_admin bypass batasan ini.

**Response (201):** User object yang baru dibuat.

**Errors:**

| Status | Message | Keterangan |
|--------|---------|------------|
| 400 | Invalid request payload | Binding/validation gagal |
| 400 | Invalid role_id | `role_id` tidak ada / soft-deleted di `core.roles` |
| 403 | Role not allowed for caller | Non-super-admin mencoba meng-assign role `super_admin` |
| 409 | Email already exists | - |
| 409 | Username already exists | - |

---

### 4. Update User

```
PUT /core/v1/users/:id
```

**Auth:** Bearer token
**Permission:** `user-management:update`

**Request:** (semua field opsional)
```json
{
  "email": "updated@tuai.com",
  "username": "updateduser",
  "full_name": "Updated Name",
  "phone": "+6289876543210",
  "avatar_url": "https://example.com/avatar.jpg",
  "is_active": false,
  "role_id": "00000000-0000-0000-0000-000000000003"
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `email` | string | Email baru (validasi unique) |
| `username` | string | Username baru (validasi unique) |
| `full_name` | string | Nama lengkap |
| `phone` | string | Nomor telepon |
| `avatar_url` | string | URL avatar (format URL valid) |
| `is_active` | bool | Toggle aktif/nonaktif |
| `role_id` | UUID / omit | Kalau dikirim (non-null): **mengganti semua** role user dengan role ini. Omit / `null` = role user tidak diubah. |

> Password **tidak bisa** diubah dari endpoint ini. Gunakan change password.
> Setelah role diganti via `role_id`, cache permission user tersebut di-invalidate — role baru langsung berlaku di request berikutnya walau user masih memakai token yang sama.

**Proteksi super admin:**

- `role_id` divalidasi sama seperti pada Create — lihat "Validasi `role_id`" di section Create.
- Non-super-admin tidak bisa update user yang memegang role `super_admin` → `403 Cannot manage a super admin user`.

**Response (200):** Updated user object.

**Errors:**

| Status | Message | Keterangan |
|--------|---------|------------|
| 400 | User ID is required / Invalid request payload | - |
| 400 | Invalid role_id | `role_id` tidak ada / soft-deleted di `core.roles` |
| 403 | Role not allowed for caller | Non-super-admin mencoba meng-assign role `super_admin` |
| 403 | Cannot manage a super admin user | Non-super-admin mencoba update user pemegang role `super_admin` |
| 404 | User not found (tidak ada atau sudah dihapus) | - |
| 409 | Email already exists | - |
| 409 | Username already exists | - |

---

### 5. Delete User

```
DELETE /core/v1/users/:id
```

**Auth:** Bearer token
**Permission:** `user-management:delete`

> Non-super-admin tidak bisa menghapus user yang memegang role `super_admin`.

**Response (200):**
```json
{
  "data": null,
  "message": "User deleted successfully"
}
```

**Errors:**

| Status | Message | Error code |
|--------|---------|------------|
| 400 | User ID is required | - |
| 403 | Cannot manage a super admin user | - |
| 404 | User not found (tidak ada atau sudah dihapus) | - |

---

## Self-Service Endpoints

Endpoint untuk user mengelola profil sendiri (tidak butuh permission khusus, hanya butuh login).

### 6. Get Current User (Me)

```
GET /core/v1/users/me
```

**Auth:** Bearer token

**Response (200):** User object dari user yang sedang login (berdasarkan `user_id` di JWT).

---

### 7. Update Profile (Me)

```
PUT /core/v1/users/me
```

**Auth:** Bearer token

**Request:**
```json
{
  "full_name": "John Doe Updated",
  "phone": "+6281234567890",
  "avatar_url": "https://example.com/new-avatar.jpg"
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `full_name` | string | Max 255 karakter |
| `phone` | string | Max 20 karakter |
| `avatar_url` | string | Format URL valid, max 500 karakter |

> **Tidak bisa** mengubah email, username, atau is_active dari endpoint ini.

**Response (200):** Updated user object.

---

### 8. Change Password

```
PUT /core/v1/users/me/password
```

**Auth:** Bearer token

**Request:**
```json
{
  "current_password": "oldpassword123",
  "new_password": "newpassword456"
}
```

| Field | Type | Required | Validasi |
|-------|------|----------|----------|
| `current_password` | string | Ya | Password saat ini |
| `new_password` | string | Ya | Min 8 karakter |

**Response (200):**
```json
{
  "data": null,
  "message": "Password changed successfully"
}
```

**Errors:**

| Status | Message |
|--------|---------|
| 400 | Invalid current password |
| 404 | User not found |

---

## Permission Requirements Summary

| Endpoint | Permission | Catatan |
|----------|-----------|---|
| `GET /users` | `user-management:read` | - |
| `GET /users/:id` | `user-management:read` | - |
| `POST /users` | `user-management:create` | Atomic via transaction; `role_id` divalidasi |
| `PUT /users/:id` | `user-management:update` | `role_id` mengganti semua role user; target super_admin diproteksi |
| `DELETE /users/:id` | `user-management:delete` | Target super_admin diproteksi |
| `GET /users/me` | Login saja | - |
| `PUT /users/me` | Login saja | - |
| `PUT /users/me/password` | Login saja | - |

> `super_admin` bypass semua permission check.
>
> "Atomic via transaction" = `POST /users` membungkus insert `core.users` dan `core.user_roles` dalam 1 transaction. Kalau assignment role gagal, insert user ikut rollback.
>
> "Target super_admin diproteksi" = non-super-admin yang PUT/DELETE user pemegang role `super_admin` mendapat `403 Cannot manage a super admin user`.

> Permission format di JWT: `"user-management:create"` (bukan `core.users:create`).
> Cek di frontend: `permissions.includes("user-management:create")`

---

**Last Updated:** 2026-10-21
