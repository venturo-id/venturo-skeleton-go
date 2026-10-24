# Audit Log API Contract

**Version:** v1
**Base URL:** `/core/v1/audit-logs`
**Module:** Core Audit Log
**Last Updated:** 2026-10-24

---

## ⚠️ Behaviour Change (2026-10-24) — action Google link / unlink dan foto profil, sign in lewat kode

- **Action baru:** `auth.google_linked` (`via`: `sign_in` — Google sign-in me-link akun Google ke akun dengan email yang sama; `settings` — [`POST /account/google/link`](account.md#6-link-google-account)), `auth.google_unlinked` ([`DELETE /account/google`](account.md#7-unlink-google-account)), `user.avatar_changed` dan `user.avatar_removed` ([`PUT` / `DELETE /users/me/avatar`](user.md#9-upload-avatar)).
- **`auth.sign_in` punya dua `method` baru:** `email_code` ([`POST /auth/verify-email`](auth.md#9-verify-email) sekaligus sign in) dan `password_reset` ([`POST /auth/reset-password`](auth.md#13-reset-password) sekaligus sign in).
- **`auth.email_changed`** sekarang dicatat oleh [`POST /account/email/confirm`](account.md#5-confirm-email-change) (`POST /auth/confirm-email-change` dihapus).
- **Error endpoint ini membawa `code`**: `503 service_unavailable` tanpa `MONGO_URI`, `403 forbidden` tanpa permission.

---

## ⚠️ Behaviour Change (2026-10-23) — dokumen baru: audit log

- **Endpoint baru [`GET /audit-logs`](#1-list-audit-log)** dengan permission baru **`audit_logs:read`**. Resource `audit_logs` muncul di [`GET /roles/permission-catalog`](role.md#3-get-permission-catalog); migration `000010` memberikannya ke role `administrator` dengan level `admin`. Role lain belum punya — beri lewat [`PUT /roles/:id`](role.md#5-update-role).
- **Audit log opsional, disimpan di MongoDB** (`MONGO_URI`). Tanpa itu tidak ada yang dicatat dan endpoint ini menjawab `503 Audit log is not configured`.
- **Aktivitas user ikut di [ekspor data akun](account.md#8-export-account-data)** (`activity`).

---

## Overview

Catatan siapa melakukan apa ke siapa: sign in (berhasil dan ditolak), password salah dan lock, sesi, perubahan kredensial dan email, serta perubahan user, role, dan API key. Hanya bisa dibaca — tidak ada endpoint untuk menulis, mengubah, atau menghapus entri.

**Key Features:**
- Disimpan di MongoDB, terpisah dari data aplikasi di PostgreSQL
- Filter per actor, target, action, dan rentang waktu; terbaru dulu
- Entri menyebut orang dengan **user id saja** — tidak pernah email, nama, atau login yang diketik saat sign in — dan `details` tidak berisi nilai field atau secret. Yang tetap disimpan sebagai catatan keamanan: IP dan user agent tiap request
- Ditulis di background: request tidak pernah gagal atau melambat karena audit log
- Entri kedaluwarsa otomatis setelah `AUDIT_LOG_RETENTION` (default 365 hari)

---

## Konfigurasi (operator)

| Env | Default | Keterangan |
|-----|---------|------------|
| `MONGO_URI` | - | Connection string MongoDB. Kosong = audit log mati: tidak ada yang dicatat, `GET /audit-logs` → `503`, `activity` di ekspor akun → `[]` |
| `MONGO_DATABASE` | `timebox` | Database tempat collection `audit_logs` |
| `AUDIT_LOG_RETENTION` | `8760h` (365 hari) | Umur entri (TTL index pada `at`). Nilai baru diterapkan ke index saat startup |

> Dengan `MONGO_URI` terisi, API **tidak mau start** kalau MongoDB tidak menjawab dalam 5 detik atau index-nya gagal dibuat. Tanpa `MONGO_URI` API start dengan warning `MONGO_URI is not set — the audit log records nothing`.
>
> `/ready` tidak mengecek MongoDB — audit log yang mati tidak membuat instance dianggap tidak siap.

### Cara entri ditulis

- Entri masuk antrean di memory (kapasitas 4096) dan ditulis per batch: setiap 100 entri atau setiap 1 detik. Entri biasanya muncul dalam ~1 detik setelah request-nya.
- Kalau MongoDB tidak bisa dihubungi atau lambat, batch yang gagal ditulis **dibuang** dan dicatat sebagai error di log aplikasi (`Audit log entries not written`); kalau antreannya penuh, entri baru dibuang (`Audit log queue is full; entries dropped`). Tidak ada retry — request user tetap jalan normal.
- Saat shutdown, antrean ditulis dulu sebelum koneksi ditutup — paling lama 5 detik. Entri dari request yang masih berjalan setelah itu dibuang (`Audit log is closed; entries dropped`).
- Penghapusan oleh TTL index dilakukan MongoDB di background (berjalan sekitar sekali per menit), jadi entri bisa bertahan sedikit lebih lama dari retensinya.

---

## Entry Object

```json
{
  "id": "6718a3c2e4b0a1d2c3f4a5b6",
  "at": "2026-10-23T02:15:00.412Z",
  "action": "user.updated",
  "actor_id": "550e8400-...",
  "target_type": "user",
  "target_id": "a1b2c3d4-...",
  "ip": "203.0.113.10",
  "user_agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) ...",
  "details": { "fields": ["is_active", "role_id"], "role_id": "7d1f5c2a-..." }
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `id` | string | ID entri (24 karakter hex) |
| `at` | timestamp | Waktu aksi, UTC, presisi milidetik |
| `action` | string | `<area>.<kejadian>` — lihat [Actions](#actions) |
| `actor_id` | UUID | User yang bertindak. Request tanpa login (signup, sign in, refresh, kode di email, reset password) dicatat atas nama akun yang dituju. Tidak dikirim kalau tidak ada akun sama sekali (sign in dengan login tidak dikenal) |
| `api_key_id` | string | Dikirim kalau aksi dilakukan lewat API key: **`key_id`** key itu — 8 karakter dari `tbx_<env>_<key_id>_<secret>`, sama dengan `details.key_id` di entri `api_key.*` (bukan `id` UUID-nya) |
| `target_type` | string | `user`, `role`, atau `api_key`. Tidak dikirim kalau tidak ada target |
| `target_id` | string | ID yang dikenai: user id, role id, atau `id` (UUID) API key |
| `ip` | string | IP client (dari `X-Forwarded-For` hanya lewat proxy di `TRUSTED_PROXIES`) |
| `user_agent` | string | Header `User-Agent`, dipotong di 512 byte |
| `details` | object | Fakta tentang aksi — lihat [Actions](#actions). Tidak dikirim kalau kosong |

> Field yang kosong tidak dikirim. Aksi yang berhubungan dengan sesi memakai `target_type: "user"` dengan `details.session_id` — tidak ada entri dengan `target_type: "session"`.

---

## Actions

Entri `auth.*` selalu bertarget akun yang bersangkutan (`target_type: "user"`, `target_id` = id akun); actor-nya caller, atau akun itu sendiri kalau request-nya tanpa login.

| Action | Dicatat saat | `details` |
|--------|--------------|-----------|
| `auth.sign_up` | [`POST /auth/signup`](auth.md#2-sign-up) berhasil | - |
| `auth.sign_in` | Sesi baru — sign in password, Google, verifikasi email, atau reset password | `method` (`password` / `google` / `email_code` / `password_reset`), `session_id`; Google juga `new_user` (bool) |
| `auth.sign_in_refused` | Sign in password ditolak | `reason`: `unknown_login` (tanpa actor dan target — login yang diketik tidak disimpan), `locked`, `inactive` (password benar, akun nonaktif), `email_unverified` (password benar, `EMAIL_VERIFICATION_REQUIRED=true`). Penolakan Google sign-in tidak dicatat |
| `auth.wrong_password` | Password salah | `purpose`: `sign_in`, `password_change` ([`PUT /users/me/password`](user.md#8-change-password)), `reauth` ([re-autentikasi](account.md#re-autentikasi)); `attempt` (urutan dalam hitungan lockout) |
| `auth.account_locked` | Password salah ke-`MAX_LOGIN_ATTEMPTS` mengunci akun | `locked_for` (durasi Go, mis. `30m0s`) |
| `auth.refresh_token_reused` | Refresh token yang sudah dirotasi dipakai lagi > 30 detik kemudian — semua sesi diakhiri | `session_id` (sesi token itu) |
| `auth.logout` | [`POST /auth/logout`](auth.md#6-logout-single-device) yang mengakhiri sesi (refresh token tidak dikenal / sudah di-revoke tidak dicatat) | `session_id` |
| `auth.logout_all` | [`POST /auth/logout-all`](auth.md#7-logout-all-devices) | - |
| `auth.session_ended` | [`DELETE /account/sessions/:id`](account.md#2-end-session) | `session_id` |
| `auth.password_changed` | [`PUT /users/me/password`](user.md#8-change-password) berhasil | - |
| `auth.password_reset` | [`POST /auth/reset-password`](auth.md#13-reset-password) berhasil | `claimed` (bool — `true` kalau ini [klaim akun](auth.md#klaim-akun-email-belum-terverifikasi)) |
| `auth.email_verified` | [`POST /auth/verify-email`](auth.md#9-verify-email) berhasil | - |
| `auth.email_change_requested` | [`POST /account/email`](account.md#3-request-email-change) mengirim kode (kirim ulang tidak dicatat) | - (alamat tidak disimpan) |
| `auth.email_changed` | [`POST /account/email/confirm`](account.md#5-confirm-email-change) berhasil | - (alamat tidak disimpan) |
| `auth.account_claimed` | Akun yang emailnya belum terverifikasi diklaim pemilik email | `via`: `google` atau `password_reset` (yang terakhir juga menghasilkan `auth.password_reset` dengan `claimed: true`) |
| `auth.google_linked` | Akun Google di-link ke akun yang sudah ada | `via`: `sign_in` ([Google sign-in](auth.md#4-sign-in-with-google) ke akun dengan email yang sama; kalau sekaligus klaim, ada juga `auth.account_claimed`) atau `settings` ([`POST /account/google/link`](account.md#6-link-google-account)). Akun baru dari Google tidak menghasilkan entri ini |
| `auth.google_unlinked` | [`DELETE /account/google`](account.md#7-unlink-google-account) | - |
| `auth.account_closed` | [`POST /account/delete`](account.md#9-delete-account) berhasil | - |
| `user.created` | [`POST /users`](user.md#3-create-user) | `role_id` (UUID, `null` kalau tanpa role) |
| `user.updated` | [`PUT /users/:id`](user.md#4-update-user) atau [`PUT /users/me`](user.md#7-update-profile-me) | `fields`: nama field yang dikirim di request (mis. `["is_active", "role_id"]`), tanpa nilainya. Dari `PUT /users/:id` juga `role_id` (`null` kalau tidak dikirim) |
| `user.deleted` | [`DELETE /users/:id`](user.md#5-delete-user) | - |
| `user.avatar_changed` | [`PUT /users/me/avatar`](user.md#9-upload-avatar) | - (URL tidak disimpan) |
| `user.avatar_removed` | [`DELETE /users/me/avatar`](user.md#10-remove-avatar) yang memang menghapus foto | - |
| `role.created` | [`POST /roles`](role.md#4-create-role) | `code`, `permissions` (level per resource), `is_active` |
| `role.updated` | [`PUT /roles/:id`](role.md#5-update-role) atau [`PUT /roles/:id/permissions`](role.md#6-update-permissions-only) | `code`, `permissions` dan `is_active` **setelah** perubahan |
| `role.deleted` | [`DELETE /roles/:id`](role.md#7-delete-role) | `code`, `permissions`, `is_active` |
| `api_key.created` | [`POST /api-keys`](api-key.md#1-create-api-key) | `key_id`, `environment`, `scoped_permissions` |
| `api_key.updated` | [`PATCH /api-keys/:id`](api-key.md#4-update-api-key) | `key_id`, `environment`, `scoped_permissions` (setelah perubahan) |
| `api_key.revoked` | [`DELETE /api-keys/:id`](api-key.md#5-revoke-api-key) | `key_id`, `environment`, `scoped_permissions` |

> Yang **tidak** menghasilkan entri sendiri: API key yang di-revoke karena akun diklaim, API key / sesi yang dihapus karena akun dihapus (cukup `auth.account_closed`), sesi yang diakhiri karena user dinonaktifkan (cukup `user.updated` dengan `fields` berisi `is_active`).
>
> Menghapus akun tidak menghapus entrinya: entri tetap ada sebagai catatan keamanan sampai kedaluwarsa (`AUDIT_LOG_RETENTION`). Yang tersisa adalah id akun yang sudah dianonimkan, plus `ip` dan `user_agent` request — sebutkan ini di kebijakan privasi.

---

## Endpoints

### 1. List Audit Log

```
GET /core/v1/audit-logs
```

**Auth:** Bearer token atau API key
**Permission:** `audit_logs:read`

**Query Parameters:** (semua opsional, digabung dengan AND)

| Param | Type | Default | Keterangan |
|-------|------|---------|------------|
| `page` | int | 1 | 1 – 100000 |
| `limit` | int | **20** | 1 – 100 |
| `actor_id` | UUID | - | Hanya entri oleh user ini |
| `target_type` | string | - | `user`, `role`, atau `api_key`. Aksi sesi bertarget `user`; cari sesinya lewat `details.session_id` |
| `target_id` | string | - | Max 64. Cocok persis — user id, role id, atau `id` API key |
| `action` | string | - | Max 64. Cocok persis, mis. `auth.sign_in`. Tidak divalidasi: action tidak dikenal → list kosong |
| `from` | RFC 3339 | - | `at` ≥ `from` (inklusif) |
| `to` | RFC 3339 | - | `at` < `to` (eksklusif) |

> `from=` / `to=` tanpa nilai diabaikan (tanpa batas). `from` / `to` harus timestamp lengkap, mis. `2026-10-23T00:00:00Z` atau `2026-10-23T00:00:00+07:00` (pecahan detik boleh). Di query string, `+` harus ditulis `%2B` — pakai `encodeURIComponent`. Tanggal saja (`2026-10-23`) → `400`.

Urutan: terbaru dulu (`at` turun).

**Contoh:**

```
GET /core/v1/audit-logs?target_type=user&target_id=550e8400-...          # semua yang terjadi pada satu user
GET /core/v1/audit-logs?actor_id=550e8400-...                             # semua yang dilakukan satu user
GET /core/v1/audit-logs?action=auth.sign_in_refused&from=2026-10-23T00:00:00%2B07:00
```

**Response (200):**
```json
{
  "data": [
    {
      "id": "6718a3c2e4b0a1d2c3f4a5b6",
      "at": "2026-10-23T02:15:00.412Z",
      "action": "user.updated",
      "actor_id": "550e8400-...",
      "target_type": "user",
      "target_id": "a1b2c3d4-...",
      "ip": "203.0.113.10",
      "user_agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) ...",
      "details": { "fields": ["is_active", "role_id"], "role_id": "7d1f5c2a-..." }
    },
    {
      "id": "6718a3a9e4b0a1d2c3f4a5b1",
      "at": "2026-10-23T02:14:31.007Z",
      "action": "auth.sign_in_refused",
      "ip": "198.51.100.23",
      "user_agent": "curl/8.5.0",
      "details": { "reason": "unknown_login" }
    }
  ],
  "message": "Audit log retrieved successfully",
  "meta": {
    "pagination": {
      "page": 1,
      "limit": 20,
      "total": 2,
      "total_pages": 1
    }
  }
}
```

**Errors:**

| Status | Message | Kapan |
|--------|---------|-------|
| 400 | Invalid query parameters | `errors` per field (`actor_id` bukan UUID, `target_type` di luar daftar, `limit` di luar 1–100, dst.), atau `errors.detail: ["Body or parameters could not be parsed"]` untuk `from` / `to` yang bukan RFC 3339 |
| 401 | Unauthorized | Lihat [authentication errors](../README.md#authentication-errors) |
| 403 | Forbidden — `Insufficient permissions` | Tidak punya `audit_logs:read` |
| 500 | Failed to read the audit log | MongoDB tidak bisa dihubungi |
| 503 | Audit log is not configured | `MONGO_URI` kosong |

---

## Permission Requirements Summary

| Endpoint | Permission | API key | Catatan |
|----------|-----------|---|---|
| `GET /audit-logs` | `audit_logs:read` | ✅ | API key butuh `audit_logs:read` di scope-nya (dan pemiliknya memegangnya) |

> Resource `audit_logs` hanya dipakai untuk `read`. Level `editor` / `admin` bisa diberikan, tapi tidak ada endpoint yang mengecek action lain. Super admin melewati cek permission.

---

**Last Updated:** 2026-10-24 (action `auth.google_linked`, `auth.google_unlinked`, `user.avatar_changed`, `user.avatar_removed`; `auth.sign_in` method `email_code` / `password_reset`; `auth.email_changed` dari `POST /account/email/confirm`)
**Previously:** 2026-10-23 (penulisan antrean saat shutdown dibatasi 5 detik), 2026-10-23 (dokumen baru: `GET /audit-logs`, permission `audit_logs:read`, daftar action, penyimpanan MongoDB dan retensi)
