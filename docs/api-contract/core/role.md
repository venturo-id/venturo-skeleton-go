# Role Management API Contract

**Version:** v1
**Base URL:** `/core/v1/roles`
**Module:** Core Role Management
**Last Updated:** 2026-10-21

---

## Overview

API untuk mengelola roles dengan **level-based permissions**. Permissions disimpan dalam format simple yang mudah dikelola frontend, kemudian di-expand ke actions oleh backend saat generate JWT.

**Key Features:**
- Level-based permissions: `viewer`, `editor`, `admin`
- Semua role bersifat global (system-wide) — tidak ada role per-company
- System roles tidak bisa dimodifikasi/dihapus
- Soft delete

---

## Permission System

### Level-Based Format

Permissions disimpan di database sebagai:
```json
{
  "dashboard": "viewer",
  "user-management": "admin",
  "jurnal-umum": "editor",
  "laporan-keuangan": "viewer"
}
```

### Level Definitions

| Level | Actions yang Di-expand |
|-------|----------------------|
| `viewer` | `read` |
| `editor` | `create`, `read`, `update`, `delete` |
| `admin` | `create`, `read`, `update`, `delete`, `export`, `import`, `restore` |

### Available Permission Resources

| Group | Resource ID | Keterangan |
|-------|------------|------------|
| Core | `roles` | Kelola role |
| | `user-management` | Kelola user |
| Finance | `finance.chart_of_accounts` | Chart of accounts |
| | `finance.bank_accounts` | Bank accounts |
| Rental | `rental.customers` | Kelola customer |
| | `rental.rentals` | Kelola rental/booking |
| | `rental.rental_items` | Kelola rental items |
| | `rental.item_categories` | Kelola kategori item |
| | `rental.item_pricing` | Kelola pricing item |
| | `rental.pricing_rules` | Kelola pricing rules |
| | `rental.rental_payments` | Kelola pembayaran rental |

### Cara Frontend Menggunakan

**Saat buat/edit role:** Frontend kirim format level-based langsung.

**Saat cek akses:** Cek dari JWT claims `permissions` array:
```js
// JWT permissions sudah dalam format flat
const canCreate = permissions.includes("user-management:create")
const canRead = permissions.includes("dashboard:read")
```

---

## Role Object

```json
{
  "id": "660e8400-...",
  "code": "admin",
  "name": "Administrator",
  "description": "Custom administrator role",
  "permissions": {
    "dashboard": "admin",
    "user-management": "editor",
    "laporan-keuangan": "viewer"
  },
  "is_system": false,
  "is_active": true,
  "created_by": "550e8400-...",
  "created_at": "2026-01-01T08:00:00Z",
  "updated_at": "2026-01-01T08:00:00Z"
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `id` | UUID | ID role |
| `code` | string | Kode unik global di antara role yang belum dihapus (snake_case), tidak bisa diubah setelah dibuat |
| `name` | string | Nama tampilan |
| `description` | string / null | Deskripsi |
| `permissions` | object | `{"resource": "level"}` — level: viewer/editor/admin |
| `is_system` | boolean | Role sistem (tidak bisa diubah/dihapus) |
| `is_active` | boolean | Status aktif |
| `created_by` | UUID / null | User yang membuat |
| `created_at` | timestamp | Waktu dibuat |
| `updated_at` | timestamp | Waktu terakhir diubah |

---

## Endpoints

### 1. List All Roles

```
GET /core/v1/roles
```

**Auth:** `Authorization: Bearer <access_token>`

**Query Parameters:**

| Param | Type | Default | Keterangan |
|-------|------|---------|------------|
| `page` | int | 1 | Halaman |
| `limit` | int | 10 | Max 100 |
| `search` | string | - | Cari di code atau name |

> Caller non-super-admin tidak melihat role `super_admin` di list ini.

**Response (200):**
```json
{
  "data": [
    {
      "id": "00000000-0000-0000-0000-000000000001",
      "code": "super_admin",
      "name": "Super Administrator",
      "description": "Full system access, bypasses all permission checks",
      "permissions": {},
      "is_system": true,
      "is_active": true,
      "created_by": null,
      "created_at": "2026-01-01T08:00:00Z",
      "updated_at": "2026-01-01T08:00:00Z"
    }
  ],
  "message": "Roles retrieved successfully",
  "meta": {
    "pagination": {
      "page": 1,
      "limit": 10,
      "total": 4,
      "total_pages": 1
    }
  }
}
```

---

### 2. Get Role by ID

```
GET /core/v1/roles/:id
```

**Auth:** `Authorization: Bearer <access_token>`

**Response (200):** Single role object (same structure as list item).

---

### 3. Create Role

```
POST /core/v1/roles
```

**Auth:** `Authorization: Bearer <access_token>`
**Permission:** `roles:create`

**Request:**
```json
{
  "code": "finance_manager",
  "name": "Finance Manager",
  "description": "Manages finance operations",
  "permissions": {
    "dashboard": "viewer",
    "penerimaan-kas": "editor",
    "pengeluaran-kas": "editor",
    "transfer-dana": "editor",
    "jurnal-umum": "editor",
    "laporan-keuangan": "viewer"
  },
  "is_active": true
}
```

| Field | Type | Required | Validasi |
|-------|------|----------|----------|
| `code` | string | Ya | 2-50 karakter, snake_case |
| `name` | string | Ya | 2-100 karakter |
| `description` | string | Tidak | Max 1000 karakter |
| `permissions` | object | Ya | `{"resource": "viewer/editor/admin"}` |
| `is_active` | bool | Tidak | Default `true` |

> Role yang dibuat selalu global (tidak terikat company).

**Response (201):** Role object yang baru dibuat.

**Errors:**

| Status | Message |
|--------|---------|
| 400 | Invalid request payload |
| 400 | Invalid permissions format (level tidak valid) |
| 409 | Role code already exists (code sudah dipakai role lain yang belum dihapus) |

---

### 4. Update Role

```
PUT /core/v1/roles/:id
```

**Auth:** `Authorization: Bearer <access_token>`
**Permission:** `roles:update`

**Request:** (semua field opsional)
```json
{
  "name": "Senior Finance Manager",
  "description": "Updated description",
  "permissions": {
    "dashboard": "admin",
    "penerimaan-kas": "admin",
    "pengeluaran-kas": "admin",
    "laporan-keuangan": "editor"
  },
  "is_active": true
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `name` | string | Tidak | 2-100 karakter |
| `description` | string | Tidak | Max 1000 karakter |
| `permissions` | object | Tidak | Replace semua permissions |
| `is_active` | bool | Tidak | Toggle status |

> `code` tidak bisa diubah setelah dibuat.

**Response (200):** Updated role object.

**Errors:**

| Status | Message |
|--------|---------|
| 403 | Cannot modify system role |
| 404 | Role not found |

---

### 5. Update Permissions Only

```
PUT /core/v1/roles/:id/permissions
```

**Auth:** `Authorization: Bearer <access_token>`
**Permission:** `roles:update`

**Request:**
```json
{
  "permissions": {
    "dashboard": "viewer",
    "laporan-keuangan": "editor"
  }
}
```

> Mengganti **seluruh** permissions. Permissions yang tidak disertakan akan dihapus.

**Response (200):** Updated role object.

---

### 6. Delete Role

```
DELETE /core/v1/roles/:id
```

**Auth:** `Authorization: Bearer <access_token>`
**Permission:** `roles:delete`

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
| 403 | Cannot delete system role |
| 404 | Role not found |

---

## System vs Custom Roles

| | System Role | Custom Role |
|-|-------------|--------------|
| `is_system` | `true` | `false` |
| Bisa diubah? | Tidak | Ya |
| Bisa dihapus? | Tidak | Ya |

> Assignment role ke user (`core.user_roles`) juga global — tidak ada scope company. Assign/ganti role user lewat `role_id` di [user.md](user.md).

### Default System Roles

| Code | Name | Permissions | Keterangan |
|------|------|------------|------------|
| `super_admin` | Super Administrator | `{}` (kosong) | Bypass semua permission checks via `IsSuperAdmin` flag di JWT. |
| `administrator` | Administrator | Semua resource = `admin` | Di-assign secara eksplisit (signup tidak memberi role apa pun). Tidak bisa diubah kecuali oleh super_admin. |

---

**Last Updated:** 2026-10-21
