# Role Management API Contract

**Version:** v1
**Base URL:** `/core/v1/roles`
**Module:** Core Role Management
**Last Updated:** 2026-10-23

---

## ⚠️ Behaviour Change (2026-10-23) — update hanya menulis field yang dikirim

- **`PUT /roles/:id` dan `PUT /roles/:id/permissions` hanya menulis field yang dikirim.** Sebelumnya seluruh role ditulis ulang dari data yang dibaca di awal request, jadi mengganti `name` bersamaan dengan admin lain mempersempit `permissions` bisa mengembalikan permission lama. Response berisi role seperti tersimpan sesudahnya, termasuk perubahan orang lain.

---

## ⚠️ Behaviour Change (2026-10-23) — resource `audit_logs`, audit log

- **Resource baru `audit_logs`** (dipakai hanya untuk `read`: [`GET /audit-logs`](audit-log.md#1-list-audit-log)). Muncul di [`GET /roles/permission-catalog`](#3-get-permission-catalog). Migration `000010` memberikannya ke role `administrator` dengan level `admin`; role custom belum punya — tambahkan lewat Update Role kalau perlu.
- **Create, update, dan delete role dicatat di [audit log](audit-log.md)** (`role.created`, `role.updated`, `role.deleted`) beserta `code`, `permissions`, dan `is_active` setelah aksi.

---

## ⚠️ Breaking Change (2026-10-23) — Timebox baseline

- **Resource terdaftar hanya `users` dan `roles`.** `user-management` jadi `users`; resource lain (finance, rental, dashboard, dst.) dihapus dari semua role oleh migration.
- **Permission divalidasi:** key harus resource terdaftar dan value harus `viewer` / `editor` / `admin` → selain itu `400 Invalid permissions` dengan detail yang menyebut masalahnya.
- **Endpoint baru `GET /roles/permission-catalog`** — daftar resource dan level untuk editor role di FE.
- **Akses baca** (`GET /roles`, `/roles/:id`, `/roles/permission-catalog`): salah satu dari `roles:read`, `users:create`, `users:update`.
- **Role `super_admin` tidak bisa dinonaktifkan atau diberi permission** → `403`.
- **Role sistem** (`super_admin`, `administrator`) dibuat oleh migration dengan ID tetap, bukan seeder.

---

## Overview

API untuk mengelola role dengan **level-based permissions**. Role menyimpan satu level per resource; backend meng-expand level ke action saat menghitung permission user.

**Key Features:**
- Level-based permissions: `viewer`, `editor`, `admin`
- Semua role global — tidak ada role per company
- Role sistem tidak bisa dihapus dan hanya super admin yang bisa mengubahnya
- Batas atas (ceiling): caller hanya bisa membuat / mengubah / menghapus role yang permission-nya ia pegang sendiri
- Soft delete

---

## Permission System

### Level-Based Format

Disimpan di `core.roles.permissions` sebagai object `{"<resource>": "<level>"}`:

```json
{
  "users": "editor",
  "roles": "viewer"
}
```

### Level Definitions

| Level | Actions yang Di-expand |
|-------|----------------------|
| `viewer` | `read` |
| `editor` | `create`, `read`, `update`, `delete` |
| `admin` | `create`, `read`, `update`, `delete`, `export`, `import`, `restore` |

Contoh di atas menghasilkan permission `users:create`, `users:read`, `users:update`, `users:delete`, `roles:read`. User dengan beberapa role aktif mendapat gabungannya (level tertinggi per resource menang).

### Available Permission Resources

| Resource | Keterangan |
|----------|------------|
| `users` | Akun user dan role assignment-nya |
| `roles` | Role dan permission yang diberikannya |
| `audit_logs` | Catatan sign in dan perubahan akun, role, dan API key — hanya baca. Hanya action `read` yang dicek ([audit-log.md](audit-log.md)) |

Sumber kebenaran: registry di backend, dibaca FE lewat [`GET /roles/permission-catalog`](#3-get-permission-catalog). Resource baru muncul di sana ketika modulnya ditambahkan.

### Cara Frontend Menggunakan

- **Saat buat / edit role:** ambil daftar dari `/roles/permission-catalog`, kirim object level-based.
- **Saat cek akses:** pakai flat array `permissions` dari `/auth/signin`, `/auth/google`, atau `/auth/me`:

```js
const canCreateUser = permissions.includes("users:create")
const canReadRoles = permissions.includes("roles:read")
```

---

## Role Object

```json
{
  "id": "7d1f5c2a-...",
  "code": "user_manager",
  "name": "User Manager",
  "description": "Mengelola akun user, bisa melihat role",
  "permissions": {
    "users": "editor",
    "roles": "viewer"
  },
  "is_system": false,
  "is_active": true,
  "created_by": "550e8400-...",
  "created_at": "2026-10-21T08:00:00Z",
  "updated_at": "2026-10-21T08:00:00Z"
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `id` | UUID | ID role |
| `code` | string | Kode unik di antara role yang belum dihapus. Tidak bisa diubah setelah dibuat |
| `name` | string | Nama tampilan |
| `description` | string | Deskripsi — tidak dikirim kalau kosong |
| `permissions` | object | `{"resource": "level"}`; `{}` kalau kosong |
| `is_system` | boolean | Role sistem (tidak bisa dihapus; hanya super admin yang bisa mengubah) |
| `is_active` | boolean | Role nonaktif tidak memberi permission ke pemegangnya |
| `created_by` | UUID | User yang membuat — tidak dikirim untuk role sistem |
| `created_at` | timestamp | Waktu dibuat (UTC) |
| `updated_at` | timestamp | Waktu terakhir diubah (UTC) |

---

## Endpoints

### 1. List All Roles

```
GET /core/v1/roles
```

**Auth:** Bearer token atau API key
**Permission:** salah satu dari `roles:read`, `users:create`, `users:update` (pengelola user butuh list ini untuk memilih role)

**Query Parameters:**

| Param | Type | Default | Keterangan |
|-------|------|---------|------------|
| `page` | int | 1 | 1 – 100000 |
| `limit` | int | 10 | 1 – 100 |
| `search` | string | - | Max 255. Cocok sebagian (tidak case-sensitive) di code atau name |

Urutan: terbaru dulu.

> Caller yang bukan super admin **tidak melihat** role `super_admin` di list ini (`GET /roles/:id` tetap mengembalikannya, tapi role itu hanya bisa di-assign oleh super admin).

**Response (200):**
```json
{
  "data": [
    {
      "id": "00000000-0000-0000-0000-000000000002",
      "code": "administrator",
      "name": "Administrator",
      "description": "Full access to every resource. Can manage users and custom roles.",
      "permissions": { "users": "admin", "roles": "admin", "audit_logs": "admin" },
      "is_system": true,
      "is_active": true,
      "created_at": "2026-10-23T00:00:00Z",
      "updated_at": "2026-10-23T00:00:00Z"
    }
  ],
  "message": "Roles retrieved successfully",
  "meta": {
    "pagination": {
      "page": 1,
      "limit": 10,
      "total": 2,
      "total_pages": 1
    }
  }
}
```

**Errors:**

| Status | Message |
|--------|---------|
| 400 | Invalid query parameters |
| 403 | Forbidden — `Insufficient permissions` |

---

### 2. Get Role by ID

```
GET /core/v1/roles/:id
```

**Auth:** Bearer token atau API key
**Permission:** salah satu dari `roles:read`, `users:create`, `users:update`

**Response (200):** Role object, message `Role retrieved successfully`.

**Errors:**

| Status | Message |
|--------|---------|
| 400 | Invalid id (`:id` bukan UUID) |
| 403 | Forbidden — `Insufficient permissions` |
| 404 | Role not found |

---

### 3. Get Permission Catalog

Daftar resource dan level yang bisa diberikan role — untuk membangun form editor role.

```
GET /core/v1/roles/permission-catalog
```

**Auth:** Bearer token atau API key
**Permission:** salah satu dari `roles:read`, `users:create`, `users:update`

**Response (200):**
```json
{
  "data": {
    "resources": [
      { "code": "users", "name": "Users", "description": "User accounts and their role assignments" },
      { "code": "roles", "name": "Roles", "description": "Roles and the permissions they grant" },
      { "code": "audit_logs", "name": "Audit logs", "description": "The record of sign-ins and of changes to accounts, roles and API keys (read only)" }
    ],
    "levels": [
      { "level": "viewer", "actions": ["read"] },
      { "level": "editor", "actions": ["create", "read", "update", "delete"] },
      { "level": "admin", "actions": ["create", "read", "update", "delete", "export", "import", "restore"] }
    ]
  },
  "message": "Permission catalog retrieved successfully"
}
```

---

### 4. Create Role

```
POST /core/v1/roles
```

**Auth:** Bearer token atau API key
**Permission:** `roles:create`

**Request:**
```json
{
  "code": "user_manager",
  "name": "User Manager",
  "description": "Mengelola akun user, bisa melihat role",
  "permissions": {
    "users": "editor",
    "roles": "viewer"
  },
  "is_active": true
}
```

| Field | Type | Required | Validasi |
|-------|------|----------|----------|
| `code` | string | Ya | 2–50 karakter, unik di antara role yang belum dihapus. Konvensi snake_case (tidak divalidasi) |
| `name` | string | Ya | 2–100 karakter |
| `description` | string | Tidak | Max 1000 karakter |
| `permissions` | object | Ya | `{"<resource terdaftar>": "viewer" \| "editor" \| "admin"}`. `{}` boleh |
| `is_active` | bool | Tidak | Default `true` |

> Role yang dibuat selalu global dan `is_system: false`.
>
> **Ceiling:** setiap permission (setelah level di-expand) harus dipegang caller sendiri. Mis. caller dengan `users: editor` tidak bisa membuat role `users: admin` (karena `admin` mencakup `export`/`import`/`restore`). Super admin tidak dibatasi.

**Response (201):** Role object yang baru dibuat, message `Role created successfully`.

**Errors:**

| Status | Message | Keterangan |
|--------|---------|------------|
| 400 | Invalid request payload | Validasi field gagal (`errors` per field) |
| 400 | Invalid permissions | `errors.detail` menyebut masalahnya, mis. `invalid permissions: unknown resource "dashboard"` atau `invalid permissions: level "owner" for "users" must be viewer, editor or admin` |
| 403 | Role grants permissions you do not hold | Ceiling |
| 409 | Role code already exists | `code` dipakai role lain yang belum dihapus |

---

### 5. Update Role

```
PUT /core/v1/roles/:id
```

**Auth:** Bearer token atau API key
**Permission:** `roles:update`

**Request:** (semua opsional; tidak dikirim atau `null` = tidak diubah)
```json
{
  "name": "User Manager",
  "description": "Mengelola akun user dan role",
  "permissions": {
    "users": "editor",
    "roles": "editor"
  },
  "is_active": true
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `name` | string | 2–100 karakter |
| `description` | string | Max 1000 karakter |
| `permissions` | object | **Mengganti seluruh** permissions; aturan sama dengan Create |
| `is_active` | bool | Aktif / nonaktif |

> `code` tidak bisa diubah.
>
> **Role sistem** hanya bisa diubah oleh super admin. Role `super_admin` sendiri hanya boleh diganti `name` / `description`: mengirim `permissions` atau `is_active: false` → `403`.
>
> **Ceiling:** caller harus memegang semua permission role ini **sebelum** diubah (tidak bisa mengedit / menonaktifkan role yang lebih tinggi) dan **sesudah** diubah (tidak bisa menambah permission yang tidak dimilikinya).
>
> Perubahan permission atau status berlaku di request berikutnya semua pemegang role ini.
>
> Hanya field yang dikirim yang ditulis; field lain tetap seperti tersimpan, termasuk kalau diubah orang lain di saat yang sama.

**Response (200):** Role object terbaru, message `Role updated successfully`.

**Errors:**

| Status | Message | Keterangan |
|--------|---------|------------|
| 400 | Invalid id | `:id` bukan UUID |
| 400 | Invalid request payload | Validasi field gagal |
| 400 | Invalid permissions | Resource tidak terdaftar / level tidak valid |
| 403 | Cannot modify system role | Role sistem, caller bukan super admin |
| 403 | The super_admin role cannot be deactivated or given permissions | - |
| 403 | Role grants permissions you do not hold | Ceiling |
| 404 | Role not found | - |

---

### 6. Update Permissions Only

```
PUT /core/v1/roles/:id/permissions
```

**Auth:** Bearer token atau API key
**Permission:** `roles:update`

**Request:**
```json
{
  "permissions": {
    "users": "viewer",
    "roles": "viewer"
  }
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `permissions` | object | Ya | **Mengganti seluruh** permissions — resource yang tidak disertakan dihapus. `{}` boleh |

> Aturan sama dengan Update Role: role sistem hanya oleh super admin, role `super_admin` selalu `403`, ceiling sebelum dan sesudah.

**Response (200):** Role object terbaru, message `Permissions updated successfully`.

**Errors:**

| Status | Message |
|--------|---------|
| 400 | Invalid id |
| 400 | Invalid request payload (`permissions` tidak ada) |
| 400 | Invalid permissions |
| 403 | Cannot modify system role |
| 403 | The super_admin role cannot be deactivated or given permissions |
| 403 | Role grants permissions you do not hold |
| 404 | Role not found |

---

### 7. Delete Role

```
DELETE /core/v1/roles/:id
```

**Auth:** Bearer token atau API key
**Permission:** `roles:delete`

> Soft delete: `code` bisa dipakai lagi. User yang memegang role ini langsung kehilangan permission-nya di request berikutnya.

**Response (200):**
```json
{
  "data": null,
  "message": "Role deleted successfully"
}
```

**Errors:**

| Status | Message |
|--------|---------|
| 400 | Invalid id |
| 403 | Cannot delete system role |
| 403 | Role grants permissions you do not hold (role yang dihapus lebih tinggi dari caller) |
| 404 | Role not found |

---

## System vs Custom Roles

| | System Role | Custom Role |
|-|-------------|--------------|
| `is_system` | `true` | `false` |
| Dibuat oleh | Migration | `POST /roles` |
| Bisa diubah? | Hanya oleh super admin | Ya (dengan ceiling) |
| Bisa dihapus? | Tidak | Ya (dengan ceiling) |

> Assignment role ke user (`core.user_roles`) juga global. Assign / ganti role user lewat `role_id` di [user.md](user.md#4-update-user).

### Default System Roles

| ID | Code | Name | Permissions | Keterangan |
|----|------|------|------------|------------|
| `00000000-0000-0000-0000-000000000001` | `super_admin` | Super Administrator | `{}` (dikunci) | Melewati semua cek permission. Dicek dari role aktif user saat request, bukan dari claim `is_super_admin` di JWT. Hanya super admin yang bisa meng-assign-nya. Super admin pertama dibuat oleh `seed-admin` |
| `00000000-0000-0000-0000-000000000002` | `administrator` | Administrator | Setiap resource terdaftar = `admin` | Saat ini `{"users": "admin", "roles": "admin", "audit_logs": "admin"}`; setiap modul baru menambahkan resource-nya ke role ini lewat migration |

---

**Last Updated:** 2026-10-23 (update hanya menulis field yang dikirim)
**Previously:** 2026-10-23 (resource `audit_logs` di catalog dan role `administrator`, perubahan role dicatat di audit log), 2026-10-23 (Timebox baseline: resource `users`/`roles`, validasi permission, permission catalog, role `super_admin` dikunci, role sistem dari migration), 2026-10-22 (ceiling permission, GET butuh permission, super admin dibaca live)
