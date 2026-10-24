# API Key API Contract

**Version:** v1
**Base URL:** `/core/v1/api-keys`
**Module:** Core API Keys
**Last Updated:** 2026-10-23

---

## ⚠️ Breaking Change (2026-10-23) — Timebox baseline

- **Format key baru: `tbx_<env>_<key_id>_<secret>`.** Semua key format lama (`wiz_…`) di-revoke oleh migration (`revoke_reason`: `API keys now start with tbx_; create a new key`) — buat key baru.
- **`scoped_permissions` wajib dan tidak boleh kosong.** Tidak ada lagi "kosong = warisi semua permission". Setiap entri harus permission terdaftar yang dipegang pembuatnya.
- **`GET /api-keys` memakai format list standar:** `data` berupa array + `meta.pagination` (sebelumnya `data: {api_keys, total, page, limit}`).
- **Revoke key yang sudah di-revoke** → `409 API key is already revoked`.

---

## Overview

API key memberi akses programatik (script, integrasi, server lain) atas nama seorang user, tanpa sesi login. Setiap user mengelola key-nya sendiri.

**Key Features:**
- Key dimiliki satu user; semua endpoint di sini hanya melihat key milik caller
- Scope permission eksplisit — permission efektif = scope key ∩ permission pemilik **saat request**
- IP whitelist (IP atau CIDR), rate limit per key, masa berlaku opsional
- Secret hanya ditampilkan sekali; yang disimpan hanya hash SHA-256
- Statistik pemakaian: `last_used_at`, `last_used_ip`, `total_requests`

**Semua endpoint di sini butuh sesi user** (`Authorization: Bearer <access_token>`). API key tidak bisa mengelola API key (`403 This endpoint cannot be used with an API key`) — supaya key dengan scope sempit tidak bisa membuat key yang lebih luas. Tidak ada permission khusus: setiap user yang login boleh mengelola key-nya sendiri.

---

## Format Key

```
tbx_live_k3j9x2ma_9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
│   │    │        └─ secret: 64 karakter hex (32 byte acak)
│   │    └─ key_id: 8 karakter huruf kecil / angka
│   └─ environment: live | test
└─ prefix tetap
```

- `environment` hanya label (mis. pisahkan key produksi dan uji coba); tidak ada perbedaan perilaku. Bagian `<env>` di key harus sama dengan environment key-nya.
- Key lengkap hanya dikirim **sekali**, di response Create. Setelah itu yang terlihat hanya `key_prefix` (`tbx_live_k3j9x2ma_9f86d081...`).

---

## API Key Object

```json
{
  "id": "7c9e6679-...",
  "key_prefix": "tbx_live_k3j9x2ma_9f86d081...",
  "name": "Sinkronisasi kalender",
  "description": "Job malam yang membaca daftar user",
  "environment": "live",
  "scoped_permissions": ["users:read"],
  "ip_whitelist": ["203.0.113.10", "10.20.0.0/16"],
  "rate_limit": 1000,
  "rate_limit_window": 3600,
  "expires_at": "2027-01-31T23:59:59Z",
  "last_used_at": "2026-10-23T01:00:12Z",
  "last_used_ip": "203.0.113.10",
  "total_requests": 4821,
  "is_active": true,
  "created_at": "2026-10-20T08:00:00Z",
  "updated_at": "2026-10-20T08:00:00Z"
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `id` | UUID | ID key (dipakai di path endpoint) |
| `key_prefix` | string | Awal key untuk dikenali di UI |
| `name` | string | Nama |
| `description` | string | Tidak dikirim kalau kosong |
| `environment` | string | `live` atau `test` |
| `scoped_permissions` | string[] | Permission yang boleh dipakai key |
| `ip_whitelist` | string[] | IP / CIDR yang boleh memakai key. Tidak dikirim kalau kosong = semua IP |
| `rate_limit` | int | Maksimal request per `rate_limit_window` |
| `rate_limit_window` | int | Panjang window dalam detik |
| `expires_at` | timestamp | Tidak dikirim kalau tanpa masa berlaku |
| `revoked_at` | timestamp | Dikirim kalau key sudah di-revoke |
| `last_used_at`, `last_used_ip` | timestamp, string | Pemakaian terakhir — tidak dikirim kalau belum pernah dipakai |
| `total_requests` | int | Jumlah request yang lolos validasi key |
| `is_active` | bool | `true` = belum di-revoke dan belum expired |
| `created_at`, `updated_at` | timestamp | UTC |

> Statistik pemakaian diperbarui di background, jadi bisa tertinggal sesaat.

---

## Endpoints

### 1. Create API Key

```
POST /core/v1/api-keys
```

**Auth:** Bearer token (API key → `403`)

**Request:**
```json
{
  "name": "Sinkronisasi kalender",
  "description": "Job malam yang membaca daftar user",
  "environment": "live",
  "scoped_permissions": ["users:read"],
  "ip_whitelist": ["203.0.113.10", "10.20.0.0/16"],
  "rate_limit": 1000,
  "rate_limit_window": 3600,
  "expires_at": "2027-01-31T23:59:59Z"
}
```

| Field | Type | Required | Validasi |
|-------|------|----------|----------|
| `name` | string | Ya | 1–100 karakter |
| `description` | string | Tidak | Max 500 karakter |
| `environment` | string | Tidak | `live` (default) atau `test` |
| `scoped_permissions` | string[] | Ya | 1–200 entri, masing-masing max 100 karakter. Setiap entri harus permission terdaftar (`<resource>:<action>`, mis. `users:read`) **yang dipegang caller saat ini**; super admin boleh memakai permission terdaftar apa pun. Duplikat dibuang |
| `ip_whitelist` | string[] | Tidak | Max 50 entri, masing-masing IP (v4/v6) atau CIDR. Kosong = semua IP |
| `rate_limit` | int | Tidak | 1 – 100000. Default `1000` |
| `rate_limit_window` | int | Tidak | 60 – 86400 detik. Default `3600` |
| `expires_at` | string | Tidak | RFC 3339 (mis. `2027-01-31T23:59:59Z`) dan harus di masa depan. Kosong = tidak pernah expired |

**Response (201):**
```json
{
  "data": {
    "id": "7c9e6679-...",
    "key": "tbx_live_k3j9x2ma_9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
    "key_prefix": "tbx_live_k3j9x2ma_9f86d081...",
    "name": "Sinkronisasi kalender",
    "message": "API key created successfully. Save this key securely - it will not be shown again."
  },
  "message": "API key created successfully"
}
```

> **Simpan `key` sekarang** — tidak bisa diambil lagi. Kalau hilang, revoke dan buat key baru.

**Errors:**

| Status | Message | Keterangan |
|--------|---------|------------|
| 400 | Invalid request payload | Validasi field gagal (`errors` per field) |
| 400 | Invalid API key settings | `errors.detail` menyebut masalahnya — lihat tabel di bawah |
| 403 | Forbidden — `This endpoint cannot be used with an API key` | Dipanggil dengan API key |

`errors.detail` untuk `Invalid API key settings`:

| Detail | Penyebab |
|--------|----------|
| `invalid permission scope: list at least one permission` | Update dengan `scoped_permissions: []` (di Create, `[]` sudah ditolak sebagai `Invalid request payload`) |
| `invalid permission scope: "users:approve" is not a permission` | Resource tidak terdaftar, action tidak dikenal, atau bukan format `<resource>:<action>` |
| `invalid permission scope: you do not hold "users:delete"` | Caller sendiri tidak memegang permission itu |
| `expires_at must be in the future` | `expires_at` sudah lewat |

---

### 2. List API Keys

```
GET /core/v1/api-keys
```

**Auth:** Bearer token (API key → `403`)

Hanya key milik caller, termasuk yang sudah di-revoke / expired.

**Query Parameters:**

| Param | Type | Default | Keterangan |
|-------|------|---------|------------|
| `page` | int | 1 | 1 – 100000 |
| `limit` | int | 10 | 1 – 100 |
| `environment` | string | - | `live` atau `test` |
| `is_active` | bool | - | `true` = belum di-revoke dan belum expired; `false` = sudah di-revoke atau expired |

Urutan: terbaru dulu.

**Response (200):**
```json
{
  "data": [
    {
      "id": "7c9e6679-...",
      "key_prefix": "tbx_live_k3j9x2ma_9f86d081...",
      "name": "Sinkronisasi kalender",
      "environment": "live",
      "scoped_permissions": ["users:read"],
      "rate_limit": 1000,
      "rate_limit_window": 3600,
      "total_requests": 4821,
      "is_active": true,
      "created_at": "2026-10-20T08:00:00Z",
      "updated_at": "2026-10-20T08:00:00Z"
    }
  ],
  "message": "API keys retrieved successfully",
  "meta": {
    "pagination": {
      "page": 1,
      "limit": 10,
      "total": 1,
      "total_pages": 1
    }
  }
}
```

**Errors:**

| Status | Message |
|--------|---------|
| 400 | Invalid query parameters |
| 403 | Forbidden — `This endpoint cannot be used with an API key` |

---

### 3. Get API Key by ID

```
GET /core/v1/api-keys/:id
```

**Auth:** Bearer token (API key → `403`)

**Response (200):** API key object, message `API key retrieved successfully`.

**Errors:**

| Status | Message | Keterangan |
|--------|---------|------------|
| 400 | Invalid id | `:id` bukan UUID |
| 403 | Forbidden — `This endpoint cannot be used with an API key` | - |
| 404 | API key not found | Tidak ada, atau milik user lain |

---

### 4. Update API Key

```
PATCH /core/v1/api-keys/:id
```

**Auth:** Bearer token (API key → `403`)

**Request:** (semua opsional; tidak dikirim atau `null` = tidak diubah)
```json
{
  "name": "Sinkronisasi kalender (v2)",
  "scoped_permissions": ["users:read", "roles:read"],
  "ip_whitelist": [],
  "rate_limit": 5000,
  "rate_limit_window": 3600
}
```

| Field | Type | Validasi / Keterangan |
|-------|------|------------|
| `name` | string | 1–100 karakter |
| `description` | string | Max 500 karakter |
| `scoped_permissions` | string[] | **Mengganti seluruh** scope. Tidak boleh kosong; aturan sama dengan Create (harus dipegang caller saat ini) |
| `ip_whitelist` | string[] | **Mengganti seluruh** whitelist. Max 50 IP/CIDR. `[]` = hapus whitelist (semua IP) |
| `rate_limit` | int | 1 – 100000 |
| `rate_limit_window` | int | 60 – 86400 detik |

> `environment` dan `expires_at` tidak bisa diubah. Key yang sudah di-revoke tidak bisa diaktifkan lagi.

**Response (200):** API key object terbaru, message `API key updated successfully`.

**Errors:**

| Status | Message | Keterangan |
|--------|---------|------------|
| 400 | Invalid id | `:id` bukan UUID |
| 400 | Invalid request payload | Validasi field gagal |
| 400 | Invalid API key settings | Scope tidak valid — lihat tabel di Create |
| 403 | Forbidden — `This endpoint cannot be used with an API key` | - |
| 404 | API key not found | Tidak ada, atau milik user lain |

---

### 5. Revoke API Key

```
DELETE /core/v1/api-keys/:id
```

**Auth:** Bearer token (API key → `403`)

**Request:** (opsional — body boleh kosong)
```json
{
  "reason": "Integrasi dihentikan"
}
```

| Field | Type | Required | Validasi |
|-------|------|----------|----------|
| `reason` | string | Tidak | Max 500 karakter |

**Response (200):**
```json
{
  "data": null,
  "message": "API key revoked successfully"
}
```

> Key langsung ditolak di request berikutnya (`401 API key has been revoked`). Key tidak dihapus: tetap muncul di list dengan `is_active: false` dan `revoked_at` terisi.

**Errors:**

| Status | Message | Keterangan |
|--------|---------|------------|
| 400 | Invalid id | `:id` bukan UUID |
| 400 | Invalid request payload | `reason` terlalu panjang |
| 403 | Forbidden — `This endpoint cannot be used with an API key` | - |
| 404 | API key not found | Tidak ada, atau milik user lain |
| 409 | API key is already revoked | - |

---

## Using an API Key

Kirim key lengkap di header `X-API-Key` (kalau `Authorization` juga dikirim, API key yang dipakai):

```bash
curl https://<api-host>/core/v1/users \
  -H "X-API-Key: tbx_live_k3j9x2ma_9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
```

**Apa yang boleh dilakukan key:**

- Permission efektif = `scoped_permissions` ∩ permission pemilik **saat request**. Kalau pemilik kehilangan permission (role dicabut / diubah), key ikut kehilangan permission itu.
- Key **tidak pernah** bertindak sebagai super admin, walau pemiliknya super admin.
- Endpoint session-only menolak key dengan `403`: `/auth/logout`, `/auth/logout-all`, `/auth/me`, `PUT /users/me`, `PUT /users/me/password`, semua `/account/*`, dan semua `/api-keys`. `GET /users/me` bisa dipakai dan mengembalikan profil pemilik key.

**Validasi setiap request** (urut):

| Status | `errors.detail` | Kapan |
|--------|-----------------|-------|
| 401 | `Invalid API key format` | Bukan 4 bagian `tbx_<env>_<key_id>_<secret>` |
| 401 | `Invalid API key` | `key_id` tidak dikenal, secret salah, atau `<env>` tidak cocok — sengaja tidak dibedakan |
| 401 | `API key has been revoked` | Key sudah di-revoke |
| 401 | `API key has expired` | Lewat `expires_at` |
| 401 | `IP address not allowed for this API key` | IP client tidak ada di `ip_whitelist` |
| 429 | `Retry after N seconds` | Rate limit key habis; header `Retry-After` berisi detik sampai window berikutnya |
| 401 | `User account is not active` | Pemilik key dinonaktifkan atau dihapus |
| 403 | `Insufficient permissions` | Permission endpoint tidak ada di permission efektif key |

> Rate limit dihitung per key (bersama di semua replica), fixed window sepanjang `rate_limit_window`, dan hanya untuk request yang secret-nya benar.
>
> IP client diambil dari `X-Forwarded-For` hanya kalau request datang lewat proxy di `TRUSTED_PROXIES`; selain itu IP koneksi langsung.
>
> `POST /auth/logout-all`, mengakhiri sesi, dan ganti password **tidak** me-revoke API key. Revoke manual lewat [Revoke API Key](#5-revoke-api-key), atau nonaktifkan pemiliknya. Pemilik yang [menghapus akunnya](account.md#9-delete-account) ikut menghapus semua key-nya.
>
> Create, update, dan revoke dicatat di [audit log](audit-log.md) (`api_key.*`, dengan `key_id`, `environment`, `scoped_permissions`). Request yang dibuat lewat key tercatat dengan `api_key_id` = `key_id` key itu. Daftar semua key milik user (termasuk yang sudah di-revoke) juga ada di [ekspor data akun](account.md#8-export-account-data).

---

**Last Updated:** 2026-10-23 (`/account/*` menolak API key, key ikut terhapus saat pemilik menghapus akunnya, create / update / revoke dicatat di audit log)
**Previously:** 2026-10-23 (dokumen baru: format `tbx_`, scope wajib dan dibatasi permission pemilik, list dengan `meta.pagination`, revoke dua kali → 409)
