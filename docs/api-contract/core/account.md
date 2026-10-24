# Account Self-Service API Contract

**Version:** v1
**Base URL:** `/core/v1/account`
**Module:** Core Authentication (self-service akun)
**Last Updated:** 2026-10-24

---

## ⚠️ Breaking / Behaviour Change (2026-10-24) — kode untuk ganti email, re-autentikasi Google tanpa Firebase, link / unlink Google

- **Ganti email dikonfirmasi dengan kode 6 digit**, bukan link. [`POST /account/email`](#3-request-email-change) mengirim kode ke alamat baru dan menjawab `{new_email, expires_in, resend_available_in}` (sebelumnya `data: null`). User yang sedang login memasukkan kodenya di [`POST /account/email/confirm`](#5-confirm-email-change) `{code}` → `{user}`. Kirim ulang tanpa re-autentikasi: [`POST /account/email/resend`](#4-resend-email-change-code). `POST /auth/confirm-email-change` dan halaman FE `/confirm-email-change` tidak dipakai lagi.
- **Re-autentikasi Google memakai objek `google`** `{code, code_verifier, redirect_uri, platform}` — authorization code dari Google sign-in yang baru (PKCE), sama dengan [`POST /auth/google`](auth.md#4-sign-in-with-google). Field `google_id_token` (Firebase ID token) dihapus; mengirimnya saja → `400 reauth_required`. Lihat [Re-autentikasi](#re-autentikasi).
- **Endpoint baru:** [`POST /account/google/link`](#6-link-google-account) (butuh password saat ini) dan [`DELETE /account/google`](#7-unlink-google-account). Pemilik akun dikirimi email setiap kali akun Google di-link atau di-unlink.
- **Setiap error membawa `code`**, dan `429` membawa `retry_after` (kecuali `account_locked`, lihat [Error re-autentikasi](#error-re-autentikasi)). Error re-autentikasi tidak pernah memakai `token_expired` / `session_ended`: sesi tetap berlaku.
- **`POST /account/email` menjawab `429 resend_cooldown`** kalau kode ganti email baru saja dikirim (`OTP_RESEND_COOLDOWN`, default 60 detik) — dicek **sebelum** re-autentikasi, jadi code Google tidak terbuang.
- **`POST /account/delete`** juga menghapus foto profil di R2, dan menghapus cookie `tb_refresh` kalau request membawa `X-Timebox-Client: web`.

---

## ⚠️ Behaviour Change (2026-10-23) — link ganti email batal saat akses akun berganti

- **Link ganti email yang belum diikuti batal** saat password diganti ([`PUT /users/me/password`](user.md#8-change-password)) atau direset ([`POST /auth/reset-password`](auth.md#13-reset-password)), dan saat akun diklaim lewat Google — sama seperti link verifikasi dan reset yang belum dipakai. Orang yang memegang akun sebelumnya (mis. pendaftar dengan alamat orang lain, atau yang tahu password lama) tidak bisa lagi memakai link yang dimintanya untuk merebut akun kembali. Kalau masih perlu, minta link baru setelahnya. *(Sejak 2026-10-24 berlaku untuk kode.)*
- **`400 Confirm it is you` juga muncul kalau password atau email akun berubah di tengah request** — mis. password direset bersamaan dengan `POST /account/email` atau `POST /account/delete`. Ulangi dengan password yang sekarang.
- **Mengakhiri sesi yang sedang di-refresh** (di device lain, pada saat yang sama) tetap mengakhirinya: token baru hasil refresh itu ikut di-revoke.

---

## ⚠️ Behaviour Change (2026-10-23) — dokumen baru: sesi, ganti email, ekspor data, hapus akun

- **Endpoint baru di `/core/v1/account`:** daftar sesi dan mengakhiri satu sesi, ganti email sendiri, ekspor data akun, hapus akun sendiri. Semuanya hanya untuk sesi user — API key → `403`.
- **Ganti email dan hapus akun butuh [re-autentikasi](#re-autentikasi):** password saat ini, atau Google sign-in yang baru (< 5 menit). Password salah ikut menambah hitungan lockout sign in.
- **Ganti email dikonfirmasi lewat link ke alamat baru**, yang dikirim FE ke endpoint publik `POST /auth/confirm-email-change`. *(Diganti kode sejak 2026-10-24.)*
- **Terkait:** `POST /auth/logout` sekarang juga mengakhiri access token sesinya, dan access token membawa claim `sid` (id sesi) — lihat [auth.md](auth.md#jwt-claims-structure).

---

## Overview

Endpoint untuk user mengelola akunnya sendiri: sesi sign in di tiap device, alamat email, akun Google yang ter-link, salinan datanya, dan penghapusan akun. Tidak butuh permission — cukup login.

**Key Features:**
- Daftar sesi aktif per device (device, IP, waktu sign in, aktivitas terakhir) dan mengakhiri satu sesi — access token sesi itu ikut mati
- Ganti email dengan kode 6 digit yang dikirim ke alamat baru; akun tetap memakai alamat lama sampai kodenya dimasukkan
- Link dan unlink akun Google (satu akun Google per akun Timebox)
- Ekspor semua data akun dalam satu JSON (profil, role, akun Google, sesi, API key, aktivitas)
- Hapus akun sendiri: data pribadi dihapus, akun di-soft delete, semua token berhenti
- Re-autentikasi untuk perubahan sensitif, supaya access token curian saja tidak cukup

**Semua endpoint di sini butuh sesi user** (`Authorization: Bearer <access_token>`). API key → `403 forbidden` (`This endpoint cannot be used with an API key`).

Yang dicatat di [audit log](audit-log.md): `auth.session_ended`, `auth.email_change_requested`, `auth.email_changed`, `auth.google_linked` (`via: "settings"`), `auth.google_unlinked`, `auth.account_closed`, dan `auth.wrong_password` (`purpose: "reauth"`).

---

## Re-autentikasi

`POST /account/email` dan `POST /account/delete` bisa menyerahkan akun (atau datanya) ke orang lain, jadi sesi saja tidak cukup: caller membuktikan lagi bahwa ia pemilik akun. Body-nya membawa **tepat satu** dari:

| Field | Type | Validasi | Keterangan |
|-------|------|----------|------------|
| `current_password` | string | Max 72 karakter | Password akun saat ini |
| `google` | object | Lihat di bawah | Google sign-in yang **baru saja** dilakukan, dengan akun Google yang **ter-link ke akun ini** |

Objek `google` — hasil [alur Google sign-in](auth.md#4-sign-in-with-google) (PKCE) yang sama, tapi dikirim ke sini, bukan ke `POST /auth/google`:

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `code` | string | Ya | Authorization code dari redirect Google, max 2048 |
| `code_verifier` | string | Ya | PKCE verifier, 43–128 karakter |
| `redirect_uri` | string | Ya | Persis sama dengan yang dipakai saat otorisasi, max 2048 |
| `platform` | string | Ya | `desktop` atau `web` |

```json
{
  "google": {
    "code": "4/0AeanS0bX...",
    "code_verifier": "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
    "redirect_uri": "http://127.0.0.1:53682/oauth/google",
    "platform": "desktop"
  }
}
```

- **Tidak ada atau dua-duanya dikirim** → `400 reauth_required`.
- **`current_password`** dicek di bawah lock sign in yang sama ([lockout](auth.md#1-sign-in)): password salah menambah hitungan `MAX_LOGIN_ATTEMPTS`; setelah batasnya akun terkunci selama `ACCOUNT_LOCKOUT_DURATION`, pemilik dikirimi email, sign in ditolak, dan endpoint ini menjawab `429 account_locked` untuk password benar maupun salah. Password benar me-reset hitungan.
- **Akun tanpa password** (dibuat lewat Google; `user.has_password: false` di [`/auth/me`](auth.md#8-get-me-profile--effective-permissions) dan response sign in) → `current_password` dijawab `409 no_password`; pakai `google` (atau buat password dulu lewat [`/auth/forgot-password`](auth.md#11-forgot-password)).
- **`google`** boleh dipakai akun mana pun yang punya akun Google ter-link (`user.google` tidak `null`), dengan atau tanpa password. BE menukar code-nya ke Google; akun Google lain → `403 google_account_not_linked`. Tidak menyentuh hitungan lockout. Code Google sekali pakai dan cepat kedaluwarsa: jalankan Google sign-in tepat sebelum request (mis. dengan `prompt=select_account`), dan ID token-nya harus terbit kurang dari 5 menit lalu (`reauth_stale` kalau tidak).
- **Password atau email akun berubah di tengah request** (mis. reset password bersamaan) → `400 reauth_required`; ulangi dengan bukti yang sekarang.

### Error re-autentikasi

Berlaku untuk `POST /account/email` dan `POST /account/delete`:

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | Field melebihi batas, atau objek `google` tidak lengkap / tidak valid |
| 400 | `reauth_required` | Confirm it is you — detail `Send exactly one of current_password or google` | Tidak ada bukti, dua-duanya dikirim, atau password / email akun berubah di tengah request |
| 400 | `incorrect_password` | Current password is incorrect | Password salah (dihitung ke lockout) |
| 400 | `invalid_redirect_uri` | Redirect URI is not allowed | `google.redirect_uri` tidak diizinkan untuk platform-nya (lihat [auth.md](auth.md#4-sign-in-with-google)) |
| 401 | `google_auth_failed` | Google sign-in failed — detail `Try signing in with Google again` | Google menolak code (sudah dipakai, kedaluwarsa, verifier / redirect tidak cocok), atau ID token tidak valid |
| 401 | `reauth_stale` | Sign in with Google again — detail `The Google sign-in must be less than 5 minutes old` | ID token Google terbit lebih dari 5 menit lalu |
| 403 | `google_account_not_linked` | This Google account is not linked to your account | Akun Google itu tidak ter-link ke akun ini (belum pernah, atau milik akun lain) |
| 409 | `no_password` | This account has no password — detail `It signs in with Google only; set a password through forgot-password` | `current_password` dikirim untuk akun tanpa password |
| 429 | `account_locked` | Too many incorrect passwords — detail `Try again later, or reset your password` | Akun sedang terkunci. **Tanpa** `retry_after` / header `Retry-After`: lama lock ada di email ke pemilik akun |
| 503 | `google_not_configured` | Google sign-in is not configured | `google` dikirim untuk platform yang belum punya OAuth client di BE |

> Error di atas **bukan** tanda sesi berakhir — tidak ada yang memakai `token_expired` atau `session_ended`. Sesi berakhir hanya dengan `code` `session_ended` / `user_inactive` (lihat [authentication errors](../README.md#authentication-errors)).

---

## Session Object

```json
{
  "id": "3f1c2b9e-...",
  "device": {
    "name": "Chrome on macOS",
    "type": "desktop",
    "os": "macOS",
    "browser": "Chrome"
  },
  "ip_address": "203.0.113.10",
  "signed_in_at": "2026-10-21T08:00:00Z",
  "last_active_at": "2026-10-23T02:15:00Z",
  "expires_at": "2026-10-30T02:15:00Z",
  "current": true
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `id` | UUID | ID sesi — sama dengan claim `sid` di access token. Dipakai di `DELETE /account/sessions/:id` |
| `device` | object | Ditebak dari `User-Agent` saat sign in: `name` (mis. `Chrome on macOS`), `type` (`desktop` / `mobile` / `tablet`), `os`, `browser` (`Unknown` kalau tidak dikenali; aplikasi desktop — UA berisi `timebox/` atau `Electron/` — jadi `Timebox Desktop`, mis. `Timebox Desktop on macOS`). Field kosong tidak dikirim |
| `ip_address` | string | IP client saat sign in (dari `X-Forwarded-For` hanya lewat proxy di `TRUSTED_PROXIES`). Tidak dikirim kalau kosong |
| `signed_in_at` | timestamp | Waktu sign in sesi ini |
| `last_active_at` | timestamp | Refresh terakhir (atau sign in kalau belum pernah refresh). Klien aktif me-refresh setiap umur access token, jadi ini perkiraan aktivitas terakhir (selisih ≤ ~15 menit) |
| `expires_at` | timestamp | Kapan sesi berakhir kalau tidak di-refresh lagi (`JWT_REFRESH_EXPIRATION` sejak `last_active_at`) |
| `current` | bool | `true` untuk sesi yang membuat request ini |

> Device dan IP adalah milik **sign in**-nya; refresh tidak mengubahnya. Sesi dari verifikasi email dan reset password juga sign in.

---

## Endpoints

### 1. List Sessions

Sesi aktif milik caller: sudah sign in, belum logout / diakhiri, dan belum kedaluwarsa.

```
GET /core/v1/account/sessions
```

**Auth:** Bearer token (API key → `403`)

**Response (200):**
```json
{
  "data": [
    {
      "id": "3f1c2b9e-...",
      "device": { "name": "Chrome on macOS", "type": "desktop", "os": "macOS", "browser": "Chrome" },
      "ip_address": "203.0.113.10",
      "signed_in_at": "2026-10-21T08:00:00Z",
      "last_active_at": "2026-10-23T02:15:00Z",
      "expires_at": "2026-10-30T02:15:00Z",
      "current": true
    },
    {
      "id": "8a6d0f41-...",
      "device": { "name": "Safari on iOS", "type": "mobile", "os": "iOS", "browser": "Safari" },
      "ip_address": "198.51.100.7",
      "signed_in_at": "2026-10-18T12:40:00Z",
      "last_active_at": "2026-10-22T13:05:00Z",
      "expires_at": "2026-10-29T13:05:00Z",
      "current": false
    }
  ],
  "message": "Sessions retrieved successfully"
}
```

> `data` adalah array tanpa pagination (tanpa `meta`), urut `last_active_at` terbaru dulu. `[]` kalau tidak ada.
>
> Access token yang terbit sebelum 2026-10-23 tidak punya `sid`, jadi tidak ada sesi yang `current: true` sampai token itu di-refresh.

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 401 | `unauthorized` / `token_expired` / `session_ended` / `user_inactive` | Unauthorized | Lihat [authentication errors](../README.md#authentication-errors) |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` | Dipanggil dengan API key |

---

### 2. End Session

Akhiri satu sesi caller — di device lain, atau device ini.

```
DELETE /core/v1/account/sessions/:id
```

**Auth:** Bearer token (API key → `403`)

**Response (200):**
```json
{
  "data": null,
  "message": "Session ended"
}
```

> Refresh token sesi itu di-revoke, dan mulai request berikutnya access token dengan `sid` sesi itu → `401 session_ended`. Sesi lain tidak terpengaruh.
>
> Mengakhiri sesi sendiri (`current: true`) sama dengan [`POST /auth/logout`](auth.md#6-logout-single-device): FE buang kedua token dan arahkan ke halaman login (cookie `tb_refresh` klien web tidak dihapus di sini — panggil `/auth/logout` kalau perlu). Mengakhiri semua sesi sekaligus: [`POST /auth/logout-all`](auth.md#7-logout-all-devices).
>
> API key milik user **tidak** ikut di-revoke.

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `bad_request` | Invalid id — detail `must be a UUID` | `:id` bukan UUID |
| 401 | `unauthorized` / `token_expired` / `session_ended` / `user_inactive` | Unauthorized | Lihat [authentication errors](../README.md#authentication-errors) |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` | Dipanggil dengan API key |
| 404 | `session_not_found` | Session not found | Sesi tidak dikenal, milik user lain, sudah berakhir, atau sudah kedaluwarsa |

---

### 3. Request Email Change

Minta pindah ke alamat email baru. Kode 6 digit dikirim ke alamat **baru**; akun tetap memakai alamat lama sampai kode itu dimasukkan di [`POST /account/email/confirm`](#5-confirm-email-change).

```
POST /core/v1/account/email
```

**Auth:** Bearer token (API key → `403`)

**Request:**
```json
{
  "new_email": "rina.wijaya@timebox.id",
  "current_password": "Kalender-Rapi-7"
}
```

atau, untuk konfirmasi lewat Google:

```json
{
  "new_email": "rina.wijaya@timebox.id",
  "google": {
    "code": "4/0AeanS0bX...",
    "code_verifier": "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
    "redirect_uri": "https://app.timebox.id/oauth/google",
    "platform": "web"
  }
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `new_email` | string | Ya | Format email, max 254. Tidak case-sensitive (disimpan lowercase) |
| `current_password` | string | Salah satu | Lihat [Re-autentikasi](#re-autentikasi) |
| `google` | object | Salah satu | Lihat [Re-autentikasi](#re-autentikasi) |

**Response (200):**
```json
{
  "data": {
    "new_email": "rina.wijaya@timebox.id",
    "expires_in": 900,
    "resend_available_in": 60
  },
  "message": "Enter the code sent to your new email. Until then your account keeps its current address."
}
```

| Field | Keterangan |
|-------|------------|
| `new_email` | Alamat tujuan kode (lowercase) |
| `expires_in` | Umur kode dalam detik (`OTP_EXPIRATION`) |
| `resend_available_in` | Detik sampai [kirim ulang](#4-resend-email-change-code) boleh dipakai |

Urutan cek: `new_email` sama dengan alamat sekarang → `400 email_unchanged` → kode ganti email dikirim kurang dari `OTP_RESEND_COOLDOWN` lalu → `429 resend_cooldown` (keduanya **sebelum** re-autentikasi: tidak dihitung ke lockout dan code Google tidak terpakai) → [re-autentikasi](#re-autentikasi) → alamat sudah dipakai akun lain → `409 email_taken` → kuota per jam → `429 email_quota_exceeded` → kode dikirim.

> **Kode:** 6 digit, dikirim ke `new_email`, berlaku `OTP_EXPIRATION` (default 15 menit), mati setelah `OTP_MAX_ATTEMPTS` tebakan salah. Aturan lengkap: [Kode di email](auth.md#kode-di-email-otp).
>
> **Hanya permintaan terbaru yang berlaku:** permintaan baru (atau kirim ulang) membatalkan kode ganti email sebelumnya. Kode juga batal kalau email akun berubah lewat jalan lain dulu, kalau password diganti atau direset, atau kalau akun diklaim lewat Google sebelum kodenya dimasukkan.
>
> **Jeda dan kuota:** antar-kode paling cepat `OTP_RESEND_COOLDOWN` (default 60 detik) per akun; paling banyak `MAX_VERIFICATION_EMAILS` (default 5) kode ganti email per akun per jam (hitungannya terpisah dari email verifikasi). Lewat dari itu → `429` dengan `retry_after`, tidak ada kode baru, dan kode terakhir tetap berlaku.
>
> **Saat dikonfirmasi:** lihat [Confirm Email Change](#5-confirm-email-change). Alamat baru dicek lagi saat itu.
>
> Admin mengganti email user lain lewat [`PUT /users/:id`](user.md#4-update-user), tanpa kode.

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | `new_email` tidak ada / format salah, atau field melebihi batas (`errors` per field) |
| 400 | `email_unchanged` | New email is your current email | `new_email` = alamat akun sekarang |
| 401 | `unauthorized` / `token_expired` / `session_ended` / `user_inactive` | Unauthorized | Lihat [authentication errors](../README.md#authentication-errors) |
| 401 | `user_inactive` | User account is not active | Akun dinonaktifkan / dihapus di tengah jalan |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` | Dipanggil dengan API key |
| 409 | `email_taken` | Email already exists | `new_email` dipakai akun lain (dicek setelah re-autentikasi) |
| 429 | `resend_cooldown` | A code was sent moments ago — detail `Wait before asking for another` | Kode ganti email dikirim kurang dari `OTP_RESEND_COOLDOWN` lalu; `retry_after` |
| 429 | `email_quota_exceeded` | Too many emails requested — detail `Try again later` | Kuota per jam habis; `retry_after` |
| … | | | Semua [error re-autentikasi](#error-re-autentikasi) |

---

### 4. Resend Email Change Code

Kirim kode baru untuk ganti email yang sedang menunggu, ke alamat baru yang sama. **Tanpa re-autentikasi** — permintaannya sudah dibuktikan di [`POST /account/email`](#3-request-email-change).

```
POST /core/v1/account/email/resend
```

**Auth:** Bearer token (API key → `403`)

**Request:** Body kosong

**Response (200):**
```json
{
  "data": {
    "new_email": "rina.wijaya@timebox.id",
    "expires_in": 900,
    "resend_available_in": 60
  },
  "message": "A new code has been sent to your new email"
}
```

> **Menunggu** = kode ganti email terakhir belum dipakai dan belum kedaluwarsa, dan email akun belum berubah sejak diminta. Kode yang mati karena tebakan salah **boleh** dikirim ulang: kodenya pergi ke alamat yang dipilih pemilik sesi, dan permintaannya sudah lolos re-autentikasi. Selain itu → `409 email_change_not_pending`: mulai lagi dari `POST /account/email` (dengan re-autentikasi). Penolakan `409` tidak memulai jeda kirim ulang dan tidak memakai kuota.
>
> Kode baru membatalkan kode sebelumnya. Urutan cek: jeda kirim ulang (`429 resend_cooldown`) → kuota per jam (`429 email_quota_exceeded`) → ada yang menunggu (`409`).

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 401 | `unauthorized` / `token_expired` / `session_ended` / `user_inactive` | Unauthorized | Lihat [authentication errors](../README.md#authentication-errors) |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` | Dipanggil dengan API key |
| 409 | `email_change_not_pending` | No email change is waiting for a code — detail `Start the email change again` | Tidak ada ganti email yang kodenya belum dipakai dan belum kedaluwarsa |
| 429 | `resend_cooldown` | A code was sent moments ago — detail `Wait before asking for another` | `retry_after` |
| 429 | `email_quota_exceeded` | Too many emails requested — detail `Try again later` | `retry_after` |

---

### 5. Confirm Email Change

Pindahkan akun ke alamat baru dengan kode yang dikirim ke alamat itu.

```
POST /core/v1/account/email/confirm
```

**Auth:** Bearer token (API key → `403`) — kode dimasukkan oleh user yang sedang login, di aplikasi yang memintanya

**Request:**
```json
{
  "code": "731905"
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `code` | string | Ya | Tepat 6 digit angka |

**Response (200):**
```json
{
  "data": {
    "user": {
      "id": "550e8400-...",
      "email": "rina.wijaya@timebox.id",
      "username": "rina",
      "full_name": "Rina Wijaya",
      "avatar_url": null,
      "timezone": "Asia/Jakarta",
      "has_password": true,
      "is_email_verified": true,
      "google": null
    }
  },
  "message": "Email changed successfully"
}
```

`user` adalah [user object](auth.md#user-object) dengan email baru.

Efek:

- Email akun diganti ke alamat baru dan ditandai **terverifikasi** (kodenya dikirim ke alamat itu).
- Alamat lama dikirimi email pemberitahuan yang menyebut alamat baru — kalau bukan pemiliknya yang mengganti, ia tahu dan bisa menghubungi support.
- Kode verifikasi dan reset password yang masih tertunda untuk alamat lama tidak berlaku lagi.
- Sign in sesudahnya memakai alamat baru; alamat lama tidak dikenal lagi.
- **Sesi tidak diakhiri** dan API key tetap. Claim `email` di access token yang sudah terbit tetap alamat lama sampai refresh.

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | `code` tidak ada / bukan 6 digit |
| 400 | `invalid_code` | Invalid code | Kode salah, atau kode lama yang sudah diganti lewat kirim ulang / permintaan baru (dihitung sebagai tebakan salah); `data: {"attempts_left": 4}` |
| 400 | `code_expired` | This code has expired — detail `Ask for a new code` | Tidak ada ganti email yang menunggu: kode terbaru kedaluwarsa atau sudah dipakai, kode dibatalkan (ganti / reset password, klaim akun), tidak pernah diminta, atau email akun berubah sejak diminta |
| 401 | `unauthorized` / `token_expired` / `session_ended` / `user_inactive` | Unauthorized | Lihat [authentication errors](../README.md#authentication-errors) |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` | Dipanggil dengan API key |
| 409 | `email_taken` | Email already exists | Alamat baru keburu dipakai akun lain sejak kode diminta. Tidak ada yang berubah, dan kode tetap bisa dipakai (sampai kedaluwarsa) kalau alamat itu bebas lagi |
| 429 | `too_many_attempts` | Too many wrong codes — detail `Ask for a new code` | Kode mati karena `OTP_MAX_ATTEMPTS` tebakan salah. `retry_after` = detik sampai kode baru boleh diminta: lewat [kirim ulang](#4-resend-email-change-code) (tanpa re-autentikasi, selama permintaannya belum kedaluwarsa) atau mulai lagi dari [Request Email Change](#3-request-email-change) |

---

### 6. Link Google Account

Hubungkan akun Google ke akun ini, supaya bisa sign in dengan Google.

```
POST /core/v1/account/google/link
```

**Auth:** Bearer token (API key → `403`)

**Request:** password saat ini + hasil [alur Google sign-in](auth.md#4-sign-in-with-google) (PKCE):

```json
{
  "current_password": "Kalender-Rapi-7",
  "code": "4/0AeanS0bX...",
  "code_verifier": "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
  "redirect_uri": "http://127.0.0.1:53682/oauth/google",
  "platform": "desktop"
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `current_password` | string | Ya | Password akun saat ini, max 72. **Selalu wajib**: sesi saja tidak boleh bisa menambah jalan masuk ke akun. Dicek di bawah [lockout](auth.md#1-sign-in) yang sama |
| `code`, `code_verifier`, `redirect_uri`, `platform` | | Ya | Sama dengan [`POST /auth/google`](auth.md#4-sign-in-with-google) |

**Response (200):** `{ "user": { ... } }` — [user object](auth.md#user-object) dengan `google` terisi, message `Google account linked`.

Urutan cek: password (`no_password`, `incorrect_password`, `account_locked`) → penukaran code ke Google → akun ini sudah punya akun Google (`409 google_already_linked`) → akun Google itu milik akun lain (`409 google_account_in_use`) → link.

> - Email akun Google tidak harus sama dengan email akun Timebox.
> - Alamat akun dikirimi email "akun Google ditambahkan" (menyebut email akun Google-nya). Kalau akun belum punya foto profil, foto Google disalin ke R2.
> - Akun tanpa password selalu sudah punya akun Google ter-link; tidak ada yang perlu di-link.
> - FE: cek `user.google == null` sebelum memulai alur Google — `google_already_linked` baru diketahui setelah code Google terpakai.

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | Field wajib tidak ada / tidak valid |
| 400 | `incorrect_password` | Current password is incorrect | Password salah (dihitung ke lockout) |
| 400 | `reauth_required` | Confirm it is you | Password atau email akun berubah di tengah request |
| 400 | `invalid_redirect_uri` | Redirect URI is not allowed | `redirect_uri` tidak diizinkan untuk platform-nya |
| 401 | `google_auth_failed` | Google sign-in failed — detail `Try signing in with Google again` | Google menolak code, atau ID token tidak valid / tanpa email |
| 401 | `unauthorized` / `token_expired` / `session_ended` / `user_inactive` | Unauthorized | Lihat [authentication errors](../README.md#authentication-errors) |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` | Dipanggil dengan API key |
| 409 | `no_password` | This account has no password | Akun tanpa password |
| 409 | `google_already_linked` | A Google account is already linked — detail `Unlink it first` | Akun ini sudah me-link akun Google |
| 409 | `google_account_in_use` | This Google account is linked to another account | Akun Google itu sudah ter-link ke akun Timebox lain |
| 429 | `account_locked` | Too many incorrect passwords — detail `Try again later, or reset your password` | Akun terkunci (tanpa `retry_after`) |
| 503 | `google_not_configured` | Google sign-in is not configured | Platform belum punya OAuth client di BE |

---

### 7. Unlink Google Account

Lepaskan akun Google dari akun ini.

```
DELETE /core/v1/account/google
```

**Auth:** Bearer token (API key → `403`)

**Request:** Tanpa body. Tidak butuh re-autentikasi — yang dihapus jalan masuk, bukan ditambah; pemilik akun dikirimi email.

**Response (200):** `{ "user": { ... } }` — [user object](auth.md#user-object) dengan `google: null`, message `Google account unlinked`.

> - Akun **tanpa password** tidak bisa unlink — akunnya tidak punya jalan masuk lain → `409 no_password`. Buat password dulu lewat [`/auth/forgot-password`](auth.md#11-forgot-password).
> - Sesi yang sudah ada (termasuk yang dibuat lewat Google) **tidak** diakhiri; foto profil tetap.
> - Alamat akun dikirimi email "akun Google dilepas" (menyebut email akun Google-nya).
> - Sesudahnya, Google sign-in dengan akun Google itu diperlakukan seperti akun Google baru: kalau emailnya sama dengan email akun ini (dan diverifikasi Google), akun Google itu di-link lagi saat sign in ([skenario](auth.md#4-sign-in-with-google)).

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 401 | `unauthorized` / `token_expired` / `session_ended` / `user_inactive` | Unauthorized | Lihat [authentication errors](../README.md#authentication-errors) |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` | Dipanggil dengan API key |
| 404 | `google_not_linked` | No Google account is linked | Tidak ada akun Google yang ter-link |
| 409 | `no_password` | This account has no password | Akun tanpa password |

---

### 8. Export Account Data

Semua data yang disimpan akun tentang pemiliknya, dalam satu JSON.

```
GET /core/v1/account/export
```

**Auth:** Bearer token (API key → `403`)

**Response (200):**
```json
{
  "data": {
    "exported_at": "2026-10-23T03:00:00.123Z",
    "profile": {
      "id": "550e8400-...",
      "email": "rina@timebox.id",
      "username": "rina",
      "full_name": "Rina Wijaya",
      "phone": "+6281234567890",
      "timezone": "Asia/Jakarta",
      "has_password": true,
      "is_email_verified": true,
      "email_verified_at": "2026-10-20T08:05:00Z",
      "last_login_at": "2026-10-23T02:15:00Z",
      "created_at": "2026-10-20T08:00:00Z",
      "updated_at": "2026-10-23T02:15:00Z"
    },
    "roles": ["user_manager"],
    "linked_accounts": [
      {
        "provider": "google",
        "email": "rina.wijaya@gmail.com",
        "linked_at": "2026-10-21T09:00:00Z",
        "last_login_at": "2026-10-22T07:30:00Z"
      }
    ],
    "sessions": [
      {
        "id": "3f1c2b9e-...",
        "device": { "name": "Chrome on macOS", "type": "desktop", "os": "macOS", "browser": "Chrome" },
        "ip_address": "203.0.113.10",
        "signed_in_at": "2026-10-21T08:00:00Z",
        "last_active_at": "2026-10-23T02:15:00Z",
        "expires_at": "2026-10-30T02:15:00Z",
        "current": true
      }
    ],
    "api_keys": [
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
    "activity": [
      {
        "id": "6718a3c2e4b0a1d2c3f4a5b6",
        "at": "2026-10-23T02:15:00.412Z",
        "action": "auth.sign_in",
        "actor_id": "550e8400-...",
        "target_type": "user",
        "target_id": "550e8400-...",
        "ip": "203.0.113.10",
        "user_agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) ...",
        "details": { "method": "password", "session_id": "3f1c2b9e-..." }
      }
    ]
  },
  "message": "Account data exported"
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `exported_at` | timestamp | Waktu ekspor (UTC) |
| `profile` | object | `id`, `email`, `username`, `full_name`, `phone`, `avatar_url` (tiga terakhir tidak dikirim kalau kosong), `timezone`, `has_password` (`false` = akun hanya bisa sign in lewat Google), `is_email_verified`, `email_verified_at`, `last_login_at` (tidak dikirim kalau kosong), `created_at`, `updated_at` |
| `roles` | string[] | Kode role aktif. `[]` kalau tidak ada |
| `linked_accounts` | object[] | Akun sign in eksternal yang ter-link: `provider` (`google`), `email` (email akun Google, tidak dikirim kalau kosong), `linked_at`, `last_login_at` (tidak dikirim kalau belum pernah). `[]` kalau tidak ada |
| `sessions` | object[] | Sama dengan [`GET /account/sessions`](#1-list-sessions) |
| `api_keys` | object[] | Semua API key milik user, termasuk yang sudah di-revoke / expired, dalam bentuk [API key object](api-key.md#api-key-object), terbaru dulu. **Secret tidak pernah ada** — backend hanya menyimpan hash-nya |
| `activity` | object[] | Sampai **1000** entri [audit log](audit-log.md#entry-object) terbaru dengan `actor_id` = user ini, terbaru dulu |

> **`activity`** berisi yang dilakukan user ini, termasuk lewat API key, dan percobaan sign in ke akunnya (request tanpa login dicatat atas nama akun yang dituju). Aksi admin terhadap akun ini tidak ikut (actor-nya admin). Audit log ditulis asinkron, jadi aksi dari ~1 detik terakhir bisa belum muncul. `[]` kalau audit log tidak dikonfigurasi (`MONGO_URI` kosong).
>
> FE: simpan sebagai file, mis. `new Blob([JSON.stringify(body.data, null, 2)], { type: 'application/json' })`.

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 401 | `unauthorized` / `token_expired` / `session_ended` / `user_inactive` | Unauthorized | Lihat [authentication errors](../README.md#authentication-errors) |
| 401 | `user_inactive` | User account is not active | Akun dinonaktifkan / dihapus di tengah jalan |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` | Dipanggil dengan API key |
| 500 | `internal_error` | Failed to export account data | Mis. audit log dikonfigurasi tapi MongoDB tidak bisa dihubungi |

---

### 9. Delete Account

Hapus akun sendiri. Tidak bisa dibatalkan.

```
POST /core/v1/account/delete
```

`POST`, bukan `DELETE`: request membawa body (re-autentikasi), yang dibuang sebagian klien dan proxy dari `DELETE`.

**Auth:** Bearer token (API key → `403`). Web: kirim `X-Timebox-Client: web` supaya cookie `tb_refresh` dihapus.

**Request:**
```json
{
  "current_password": "Kalender-Rapi-7"
}
```

atau `{ "google": { "code": "...", "code_verifier": "...", "redirect_uri": "...", "platform": "..." } }` — lihat [Re-autentikasi](#re-autentikasi).

**Response (200):**
```json
{
  "data": null,
  "message": "Your account has been deleted"
}
```

Efek, dalam satu transaksi:

- **Data pribadi dihapus:** email dan username diganti placeholder (`<id>@closed.invalid`, `closed-<id tanpa tanda ->`); nama, telepon, avatar, password, waktu login terakhir, dan lock dikosongkan; `timezone` kembali default. Akun dinonaktifkan dan di-soft delete — hilang dari `GET /users`.
- **Ikut dihapus:** akun Google yang ter-link, semua sesi (beserta device dan IP-nya), kode email yang tertunda, semua role assignment, dan semua API key.
- **Semua kredensial berhenti seketika:** access token → `401 user_inactive`, refresh token dan API key → `401` (tidak dikenal lagi).

Sesudah transaksi: file foto profil dihapus dari R2 (kegagalan hanya di-log), email pemberitahuan dikirim ke alamat lama akun, dan cookie `tb_refresh` dihapus (klien web).

- Alamat email dan akun Google-nya bisa dipakai mendaftar lagi — sebagai akun baru, tanpa role dan tanpa data lama.
- Entri [audit log](audit-log.md) tetap ada sampai masa retensinya habis. Entri menyebut akun dengan id, bukan email atau nama (tapi menyimpan IP dan user agent request).

> **Super admin tidak bisa menghapus akunnya sendiri** → `409 super_admin_cannot_close`, supaya sistem tidak kehilangan super admin tanpa sengaja. Super admin lain harus mencabut role `super_admin`-nya dulu ([`PUT /users/:id`](user.md#4-update-user) dengan role lain). Cek ini dilakukan setelah re-autentikasi.
>
> Admin menghapus user lain lewat [`DELETE /users/:id`](user.md#5-delete-user) — itu soft delete biasa tanpa menghapus data pribadi.

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | Field melebihi batas (`errors` per field) |
| 401 | `unauthorized` / `token_expired` / `session_ended` / `user_inactive` | Unauthorized | Lihat [authentication errors](../README.md#authentication-errors) |
| 401 | `user_inactive` | User account is not active | Akun dinonaktifkan / dihapus di tengah jalan |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` | Dipanggil dengan API key |
| 409 | `super_admin_cannot_close` | A super admin cannot delete their own account — detail `Have another super admin remove the super_admin role first` | Caller memegang role `super_admin` aktif |
| … | | | Semua [error re-autentikasi](#error-re-autentikasi) |

---

## Alur FE

### Ganti email

```
1. Form: email baru + password.
   Akun tanpa password (user.has_password = false):
   tombol "Konfirmasi dengan Google" → jalankan Google sign-in (PKCE) → objek google
2. POST /account/email { new_email, current_password | google }
     200 → form kode 6 digit "dikirim ke <new_email>"; countdown kirim ulang = resend_available_in.
           Akun masih memakai alamat lama
     409 email_taken           → alamat sudah dipakai akun lain
     429 resend_cooldown       → tunggu retry_after (code Google belum terpakai)
     429 email_quota_exceeded  → coba lagi setelah retry_after (kode terakhir masih berlaku)
     400/401/403/409/429 re-autentikasi → tampilkan message; sesi masih berlaku
3. POST /account/email/confirm { code }
     200 → simpan data.user (email baru). Claim email di access token baru ikut setelah refresh
     400 invalid_code            → "kode salah, sisa data.attempts_left kali"
     400 code_expired            → minta ulang (langkah 4) atau mulai lagi dari langkah 2
     429 too_many_attempts       → kirim ulang (langkah 4) setelah retry_after
     409 email_taken             → alamat keburu dipakai akun lain
4. Kirim ulang: POST /account/email/resend
     409 email_change_not_pending → mulai lagi dari langkah 2
```

### Akun Google

```
Link (user.google == null, user.has_password == true):
  1. Minta password, jalankan Google sign-in (PKCE)
  2. POST /account/google/link { current_password, code, code_verifier, redirect_uri, platform }
       200 → simpan data.user
       409 google_account_in_use → akun Google itu sudah dipakai akun lain

Unlink (user.google != null):
  - has_password == false → arahkan ke "buat password" (/auth/forgot-password) dulu
  - DELETE /account/google → 200: simpan data.user (google: null)
```

### Hapus akun

```
1. Tampilkan apa yang akan hilang; tawarkan unduh data dulu (GET /account/export).
2. Minta password, atau Google sign-in ulang (PKCE) untuk akun tanpa password.
3. POST /account/delete { current_password | google }   (web: X-Timebox-Client: web)
     200 → buang access token (dan refresh token desktop), arahkan ke halaman publik
     409 super_admin_cannot_close → tampilkan errors.detail
     400 / 401 / 403 / 409 / 429 re-autentikasi → tampilkan message; sesi masih berlaku
```

### Sesi di device lain

```
GET /account/sessions → tampilkan daftar; tandai current: true sebagai "Device ini"
DELETE /account/sessions/:id → hapus baris dari daftar
  (sesi sendiri → perlakukan seperti logout)
```

---

## Permission Requirements Summary

| Endpoint | Permission | API key | Catatan |
|----------|-----------|---|---|
| `GET /account/sessions` | Login saja | ❌ | - |
| `DELETE /account/sessions/:id` | Login saja | ❌ | Hanya sesi milik caller |
| `POST /account/email` | Login saja | ❌ | Re-autentikasi; kode ke alamat baru, jeda kirim ulang dan kuota per jam |
| `POST /account/email/resend` | Login saja | ❌ | Tanpa re-autentikasi; hanya kalau ada ganti email yang menunggu |
| `POST /account/email/confirm` | Login saja | ❌ | Kode 6 digit |
| `POST /account/google/link` | Login saja | ❌ | Password saat ini wajib; satu akun Google per akun |
| `DELETE /account/google` | Login saja | ❌ | Akun tanpa password → `409` |
| `GET /account/export` | Login saja | ❌ | - |
| `POST /account/delete` | Login saja | ❌ | Re-autentikasi; super admin → `409` |

---

**Last Updated:** 2026-10-24 (ganti email dengan kode 6 digit: `POST /account/email` menjawab `{new_email, expires_in, resend_available_in}`, `POST /account/email/resend`, `POST /account/email/confirm`; re-autentikasi Google dengan objek `google` (authorization code + PKCE) menggantikan `google_id_token`; `POST /account/google/link` dan `DELETE /account/google`; `code` di setiap error)
**Previously:** 2026-10-23 (link ganti email batal saat password diganti / direset atau akun diklaim; `400 Confirm it is you` saat akun berubah di tengah request; mengakhiri sesi yang sedang di-refresh), 2026-10-23 (dokumen baru: daftar & akhiri sesi, ganti email dengan konfirmasi link, ekspor data akun, hapus akun sendiri, re-autentikasi password / Google)
