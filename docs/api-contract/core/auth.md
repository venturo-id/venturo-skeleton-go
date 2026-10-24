# Authentication API Contract

**Version:** v1
**Base URL:** `/core/v1/auth`
**Module:** Core Authentication
**Last Updated:** 2026-10-24

---

## ⚠️ Breaking / Behaviour Change (2026-10-24) — kode OTP, Google OAuth langsung, cookie web, kode error

- **Link di email diganti kode 6 digit (OTP)** untuk verifikasi email, reset password, dan ganti email. Kode berlaku `OTP_EXPIRATION` (default 15 menit), sekali pakai, mati setelah `OTP_MAX_ATTEMPTS` (default 5) tebakan salah, dan hanya bisa diminta ulang setiap `OTP_RESEND_COOLDOWN` (default 60 detik) per alamat. Lihat [Kode di email](#kode-di-email-otp). Halaman FE `/verify-email?token=`, `/reset-password?token=`, `/confirm-email-change?token=` tidak dipakai lagi — ganti dengan form input kode.
- **`POST /signup`:** `username` jadi **opsional** (kosong → diturunkan dari email). Response `{user, verification: {email, expires_in, resend_available_in}}`, message `Account created. Enter the code sent to your email.` Tetap tanpa token.
- **`POST /verify-email` sekarang `{email, code}`** (bukan `{token}`) dan **langsung sign in**: response sama dengan `/signin` (token + user + roles + permissions). Sebelumnya `data: null` lalu user diarahkan ke sign in.
- **`POST /verify-email/resend` dan `POST /forgot-password`** mengembalikan `data: {expires_in, resend_available_in}`; permintaan yang terlalu cepat untuk alamat yang sama → `429 resend_cooldown` (juga untuk alamat yang tidak terdaftar).
- **Reset password jadi tiga langkah:** [`POST /forgot-password`](#11-forgot-password) `{email}` → [`POST /reset-password/verify`](#12-verify-reset-code) `{email, code}` → `{reset_token, expires_in}` (`RESET_TOKEN_EXPIRATION`, default 10 menit) → [`POST /reset-password`](#13-reset-password) `{reset_token, new_password}`. Field `token` diganti `reset_token`, dan reset sekarang **mengembalikan sesi baru** untuk client ini (sesi lain tetap diakhiri).
- **`POST /confirm-email-change` dihapus.** Ganti email dikonfirmasi oleh user yang sedang login lewat [`POST /account/email/confirm`](account.md#5-confirm-email-change) `{code}`; kode dikirim ke alamat baru. Kirim ulang: [`POST /account/email/resend`](account.md#4-resend-email-change-code).
- **Google sign-in tanpa Firebase.** BE menjadi OAuth client Google (authorization code + PKCE). Endpoint baru [`GET /google/client?platform=desktop|web`](#3-get-google-client); [`POST /google`](#4-sign-in-with-google) sekarang menerima `{code, code_verifier, redirect_uri, platform, timezone?}` — `id_token` tidak diterima lagi (`400 validation_failed`). Identity yang ter-link lewat Firebase dipindah ke id akun Google (`sub`) oleh migration `000012`: user lama tetap masuk ke akun yang sama.
- **Objek `user`** di `/signup`, `/signin`, `/google`, `/verify-email`, `/reset-password`, dan `/me` sekarang selalu berisi `id`, `email`, `username`, `full_name` (`null` kalau kosong — sebelumnya tidak dikirim), `avatar_url` (`null` kalau kosong), `timezone`, `has_password`, `is_email_verified`, dan `google` (`null` atau `{email, linked_at}`). Lihat [User object](#user-object).
- **Setiap error membawa `code`** (mis. `"code": "session_ended"`), dan setiap `429` membawa `retry_after` (detik) selain header `Retry-After`. FE bercabang pada `code`, bukan `message`. Daftar lengkap: [README](../README.md#error-codes).
- **Email belum terverifikasi → `403 email_not_verified`** (sebelumnya `401 Email not verified`) di `/signin`, `/google`, dan `/refresh`. Di `/signin` dan `/google`, `data.email` menyebut alamat tujuan kode — berguna kalau user sign in dengan username.
- **Refresh token yang tidak bisa dipakai → `401 session_ended`**, sama dengan access token dari sesi yang sudah diakhiri. Hanya `token_expired` yang berarti "refresh lalu ulangi".
- **Mode cookie untuk web:** dengan header `X-Timebox-Client: web`, refresh token dikirim sebagai cookie `tb_refresh` (HttpOnly, `SameSite=Strict`) dan tidak ada di body. `/refresh` dan `/logout` membaca cookie itu kalau body tidak membawa token. Aplikasi desktop tetap memakai body. Lihat [Klien web vs desktop](#klien-web-vs-desktop).
- **`POST /logout`:** body `refresh_token` sekarang opsional — tanpa body, cookie web, atau kalau tidak ada juga, sesi access token itu sendiri yang diakhiri.
- **Foto profil Google disalin ke R2** (`avatar_url` = `<R2_PUBLIC_URL>/avatars/...`), tidak pernah URL `lh3.googleusercontent.com`.
- **Email notifikasi baru:** akun Google di-link dan di-unlink (ke alamat akun).

---

## ⚠️ Behaviour Change (2026-10-23) — Google sign-in & verifikasi email, link batal, request bersamaan

- **Google sign-in lewat identity yang sudah ter-link ikut `EMAIL_VERIFICATION_REQUIRED`:** kalau verifikasi email akun dicabut admin → `401 Email not verified`, sama seperti sign in dengan password. Sebelumnya `200`, lalu sesinya mati di refresh pertama. *(Sejak 2026-10-24: `403 email_not_verified`.)*
- **Reset password, klaim akun, dan ganti password membatalkan semua link email yang belum dipakai**, termasuk link [ganti email](account.md#3-request-email-change): orang yang memegang akun sebelumnya tidak bisa merebutnya kembali dengan link yang ia minta. *(Sejak 2026-10-24 berlaku untuk kode.)*
- **Request yang bersamaan tidak lagi meninggalkan sesi hidup.** Sign in dengan password lama yang sedang diproses saat password direset kalah (`401 Invalid credentials`) atau sesinya ikut diakhiri; refresh yang bersamaan dengan logout-all, reset, atau [akhiri sesi](account.md#2-end-session) juga ikut diakhiri.
- **Refresh yang gagal di tengah jalan (`500`) tidak lagi menghabiskan refresh token-nya:** FE boleh mengulang dengan token yang sama. Sebelumnya token itu terpakai, dan mengulangnya setelah 30 detik dianggap pencurian (semua sesi diakhiri).
- **Toleransi selisih jam 5 detik** saat memvalidasi access token (`nbf` / `iat` / `exp`), untuk replica yang jamnya sedikit berbeda.

---

## ⚠️ Breaking / Behaviour Change (2026-10-23) — sesi per device, logout, refresh, timezone, ganti email

- **`POST /logout` sekarang juga mematikan access token sesi itu seketika.** Request berikutnya dengan access token dari sesi refresh token tersebut → `401` detail `Session has been revoked. Please sign in again.` Sebelumnya access token tetap berlaku sampai expired. Sesi lain tidak terpengaruh.
- **Claim baru `sid` di access token** — id sesi, tetap sama saat refresh. Sesi bisa dilihat dan diakhiri per device lewat [`GET /account/sessions`](account.md#1-list-sessions) dan [`DELETE /account/sessions/:id`](account.md#2-end-session). Access token yang terbit sebelum rilis ini tidak punya `sid`: tidak ikut diakhiri per sesi, habis sendiri (≤ 15 menit).
- **`POST /refresh` menolak email yang belum terverifikasi** saat `EMAIL_VERIFICATION_REQUIRED=true` → `401 Email not verified`, termasuk kalau verifikasinya dicabut admin setelah sign in. Refresh token yang dikirim ikut terpakai dan sesi itu berakhir, access token-nya juga. *(Sejak 2026-10-24: `403 email_not_verified`.)*
- **Objek `user` membawa `has_password`** (`false` = akun Google-only).
- **Field baru `timezone`** (nama zona IANA, default `Asia/Jakarta`) di objek `user` response `/signup`, `/signin`, `/google`, `/me`, dan opsional di request `/signup` dan `/google`. Zona tidak dikenal atau `Local` → `400` dengan `errors.timezone`. Lihat [Timezone](#timezone).
- **Endpoint baru:** `POST /confirm-email-change` (publik, rate limit per IP — dihapus 2026-10-24), dan grup `/core/v1/account` untuk sesi, ganti email, ekspor data, dan hapus akun ([account.md](account.md)).
- **Password salah saat [re-autentikasi](account.md#re-autentikasi)** (`POST /account/email`, `POST /account/delete`) ikut menambah hitungan [lockout](#1-sign-in).
- **Email notifikasi baru:** konfirmasi ganti email (ke alamat baru), "email diganti" (ke alamat lama, menyebut alamat baru), akun dihapus.
- **Audit log:** signup, sign in, sign in yang ditolak, password salah, lock, logout, dan perubahan akun dicatat ([audit-log.md](audit-log.md)).

---

## ⚠️ Breaking / Behaviour Change (2026-10-23) — verifikasi email, reset password, klaim akun

- **Google sign-in ke akun yang emailnya belum terverifikasi tidak lagi `409`.** Pemilik email (diverifikasi Google) **mengklaim** akun itu: identity Google di-link, password akun dihapus, email jadi terverifikasi, lock dibuka, semua sesi dan semua API key akun di-revoke. Response-nya sign in biasa (`200`, `is_new_user: false`). Pesan `409 An account with this email already exists. Sign in with your email and password.` sudah tidak ada. Lihat [Klaim akun](#klaim-akun-email-belum-terverifikasi).
- **Endpoint baru** (publik, rate limit per IP): [`POST /verify-email`](#9-verify-email), [`POST /verify-email/resend`](#10-resend-verification-code), [`POST /forgot-password`](#11-forgot-password), [`POST /reset-password`](#13-reset-password). *(Sejak 2026-10-24 memakai kode, bukan link.)*
- **Signup mengirim email verifikasi.** Message jadi `User registered successfully. Check your email to verify your address.` *(Sejak 2026-10-24: `Account created. Enter the code sent to your email.`)*
- **Reset password lewat link di akun yang emailnya belum terverifikasi juga klaim:** password pendaftar diganti, semua sesi dan semua API key di-revoke.
- **Password salah di [`PUT /users/me/password`](user.md#8-change-password) ikut menambah hitungan lockout**, dan ganti password yang berhasil me-reset hitungannya.
- **Lockout menahan tebakan paralel:** percobaan dihitung sebelum password dicek. Sebelumnya, tebakan yang dikirim bersamaan semuanya sempat dicek sebelum lock terpasang. Dalam burst seperti itu, password yang benar pun bisa ikut ditolak (`401` generik).
- **Email notifikasi baru:** akun terkunci (sekali per lock, menyebut lama lock — response sign in tetap generik), password diganti (ganti password dan reset password), welcome (verifikasi pertama dan akun Google baru).
- **`EMAIL_VERIFICATION_REQUIRED=true` sekarang bisa dipakai** (butuh SMTP; disarankan di production).

---

## ⚠️ Breaking Change (2026-10-23) — Timebox baseline

- **Permission `user-management:*` jadi `users:*`.** Resource yang terdaftar sekarang hanya `users` dan `roles`; resource lain dihapus dari semua role. Cek di FE: `permissions.includes("users:create")`.
- **Access token default 15 menit** (sebelumnya 24 jam), `iss` = `timebox-api`. Jadwalkan refresh dari `expires_in`.
- **Sign in tidak lagi membedakan alasan gagal.** Login tidak dikenal, password salah, dan akun terkunci sama-sama `401 Invalid credentials. After several failed attempts, sign-in is paused for a while.` Pesan `User account is locked. Try again later.` sudah tidak ada. `User account is not active` / `Email not verified` hanya muncul kalau password-nya benar.
- **Kebijakan password baru** untuk signup (juga create user dan ganti password): 8–72 byte, ada huruf besar, huruf kecil, dan angka, bukan password umum → selain itu `400` dengan `errors.password`.
- **`/logout` dan `/me` ikut menolak API key** (`403`), sama seperti `/logout-all`.
- **`/logout` hanya me-revoke refresh token milik caller sendiri.** Token milik user lain atau yang sudah di-revoke dibiarkan (tetap `200`).
- **`/logout-all` sekarang juga mematikan semua access token** yang sudah terbit — begitu juga ganti password, user dinonaktifkan/dihapus, dan deteksi refresh token bocor. Request berikutnya dengan token lama → `401` detail `Session has been revoked. Please sign in again.`
- **Format error:** `errors` selalu object — `errors.detail` (array) atau error per field untuk validasi (lihat [README](../README.md#errors)).

---

## Overview

Registrasi, sign in (password, Google, atau kode verifikasi email), manajemen sesi, verifikasi email, dan reset password. Sesi = access token JWT berumur pendek + refresh token opaque yang dirotasi setiap dipakai; keduanya membawa id sesi yang sama.

**Key Features:**
- Access token JWT pendek (default 15 menit) + refresh token rotation dengan deteksi token bocor
- Refresh token di body (aplikasi desktop) atau di cookie HttpOnly `tb_refresh` (aplikasi web)
- Sesi multi-device; logout satu device atau semua device. Sesi bisa dilihat dan diakhiri per device ([account.md](account.md))
- Google sign-in langsung ke Google OAuth (authorization code + PKCE), untuk desktop (redirect loopback) dan web
- Verifikasi email dan reset password dengan kode 6 digit di email (OTP)
- Timezone per user (zona IANA) untuk menampilkan dan mengelompokkan tanggal
- Account lockout setelah `MAX_LOGIN_ATTEMPTS` password salah (default 5x, dikunci `ACCOUNT_LOCKOUT_DURATION` = 30 menit)
- Rate limit per IP client di endpoint publik
- Status user, role, dan permission dibaca live setiap request (cache Redis), bukan dari isi JWT
- Akun dengan email belum terverifikasi bisa diklaim pemilik asli email itu (Google sign-in atau reset password)

---

## Response Format

Envelope standar — detail di [README](../README.md#response-format):

```json
{
  "data": { ... },
  "message": "Sign in successful"
}
```

Error — selalu membawa `code`; `data` bisa berisi detail yang dibutuhkan FE (mis. `attempts_left`), `429` membawa `retry_after`:

```json
{
  "data": { "attempts_left": 3 },
  "message": "Invalid code",
  "code": "invalid_code"
}
```

```json
{
  "data": null,
  "message": "A code was sent moments ago",
  "code": "resend_cooldown",
  "errors": { "detail": ["Wait before asking for another"] },
  "retry_after": 42
}
```

Kode error tiap endpoint ada di tabel **Errors** masing-masing; daftar semua kode: [README](../README.md#error-codes).

---

## JWT Claims Structure

Access token (HS256) berisi **identitas saja** — permissions tidak ada di sini.

```json
{
  "user_id": "550e8400-...",
  "email": "rina@timebox.id",
  "username": "rina",
  "full_name": "Rina Wijaya",
  "is_super_admin": false,
  "roles": ["user_manager"],
  "sid": "3f1c2b9e-...",
  "iss": "timebox-api",
  "sub": "550e8400-...",
  "exp": 1792745100.123,
  "nbf": 1792744200.123,
  "iat": 1792744200.123
}
```

| Claim | Type | Keterangan |
|-------|------|------------|
| `user_id` | string (UUID) | ID user (juga di `sub`) |
| `email` | string | Email (lowercase) |
| `username` | string | Username (lowercase) |
| `full_name` | string | Nama lengkap (`""` kalau kosong) |
| `is_super_admin` | bool | Status saat token dibuat — **informasi saja**, tidak dipakai untuk otorisasi |
| `roles` | string[] | Kode role aktif saat token dibuat — **informasi saja**. `[]` kalau belum punya role |
| `sid` | string (UUID) | ID sesi (satu sign in). Tetap sama saat refresh, sama dengan `id` di [`GET /account/sessions`](account.md#1-list-sessions). Sesi yang diakhiri (`/logout`, `DELETE /account/sessions/:id`) membuat semua access token dengan `sid` itu ditolak. Tidak ada di token yang terbit sebelum 2026-10-23 |
| `iss` | string | Selalu `"timebox-api"` (wajib saat verifikasi) |
| `sub` | string | `user_id` |
| `exp` | number | Expiry (Unix detik, presisi milidetik — bisa pecahan). Default `iat` + 15 menit (`JWT_EXPIRATION`) |
| `nbf`, `iat` | number | Waktu terbit (Unix detik, presisi milidetik). Server menolak token yang terbit sebelum sesi user dicabut (logout-all, ganti/reset password, dst.); presisi milidetik membuat token yang terbit di detik yang sama sebelum pencabutan ikut tertolak |

> Backend tidak memakai `is_super_admin` dan `roles` dari token untuk otorisasi — keduanya bisa basi sampai token expired. Pakai `permissions` / `is_super_admin` dari response sign in atau `/me`.

### Permissions (di response body, bukan di JWT)

Dikirim di `data.permissions` oleh setiap endpoint yang membuat sesi (`/signin`, `/google`, `/verify-email`, `/reset-password`) dan oleh `GET /me`. Flat array `"<resource>:<action>"`, tidak pernah `null` (`[]` untuk user tanpa role), urutannya tidak dijamin:

```json
["users:create", "users:read", "users:update", "users:delete", "roles:read"]
```

Role menyimpan satu level per resource (`{"users": "editor", "roles": "viewer"}`), backend meng-expand-nya:

- `viewer` → `read`
- `editor` → `create`, `read`, `update`, `delete`
- `admin` → `create`, `read`, `update`, `delete`, `export`, `import`, `restore`

Permission user = gabungan semua role aktifnya (level tertinggi per resource menang). Daftar resource: [README](../README.md#permissions).

### Kapan permissions / sesi berubah

Backend membaca status user, role, dan permission terkini di **setiap request** (di-cache di Redis, di-invalidate saat ada perubahan). Perubahan berikut berlaku di request berikutnya, tanpa menunggu token expired:

| Kejadian | Efek ke token yang sudah terbit |
|---|---|
| Permission role diubah, role user diganti | Request berikutnya memakai permission baru. Token tetap valid |
| User dinonaktifkan / dihapus | `401 user_inactive`; refresh token di-revoke |
| `POST /logout-all`, ganti password, refresh token bocor terdeteksi | Semua refresh token di-revoke; access token yang terbit sebelumnya → `401 session_ended` |
| Reset password ([`POST /reset-password`](#13-reset-password)) | Sama seperti baris di atas untuk semua sesi yang sudah ada; response reset membawa **sesi baru** untuk client yang mereset |
| Akun diklaim pemilik email (Google sign-in atau reset password pada akun yang emailnya belum terverifikasi) | Semua sesi diakhiri seperti baris di atas, **dan** semua API key akun di-revoke |
| `POST /logout`, [`DELETE /account/sessions/:id`](account.md#2-end-session) | Hanya sesi itu yang diakhiri: refresh token-nya di-revoke dan access token dengan `sid` sesi itu → `401 session_ended`. Sesi lain tetap berlaku |
| Akun dihapus pemiliknya ([`POST /account/delete`](account.md#9-delete-account)) | Semua token → `401 user_inactive`; refresh token, API key, dan akun Google yang ter-link dihapus |
| Email diganti ([`POST /account/email/confirm`](account.md#5-confirm-email-change)) | Tidak ada yang diakhiri. Claim `email` di access token tetap alamat lama sampai refresh — ambil profil terbaru dari `GET /me` |
| Akun Google di-link / di-unlink ([account.md](account.md#6-link-google-account)) | Tidak ada yang diakhiri, termasuk sesi yang dibuat lewat Google sebelum unlink |

FE yang mendapat `403 forbidden` untuk aksi yang tadinya boleh: panggil `GET /me` untuk mengambil permissions terbaru.

---

## User Object

Objek `user` di response `/signup`, `/signin`, `/google`, `/verify-email`, `/reset-password`, `/me`, dan perubahan akun di [`/account`](account.md) (`/account/email/confirm`, `/account/google/link`, `DELETE /account/google`). Semua field **selalu dikirim** — yang kosong sebagai `null`, bukan dihilangkan:

```json
{
  "id": "550e8400-...",
  "email": "rina@timebox.id",
  "username": "rina",
  "full_name": "Rina Wijaya",
  "avatar_url": "https://cdn.timebox.id/avatars/550e8400-.../3f2b9c1e-....jpg",
  "timezone": "Asia/Jakarta",
  "has_password": true,
  "is_email_verified": true,
  "google": {
    "email": "rina.wijaya@gmail.com",
    "linked_at": "2026-10-21T09:00:00Z"
  }
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `id` | UUID | ID user |
| `email` | string | Email akun (lowercase) |
| `username` | string | Username (lowercase) |
| `full_name` | string \| null | Nama lengkap |
| `avatar_url` | string \| null | Foto profil di R2: `<R2_PUBLIC_URL>/avatars/<user_id>/<id>.jpg`. Diubah lewat [`PUT /users/me/avatar`](user.md#9-upload-avatar); akun Google baru mendapat salinan foto Google-nya. Aplikasi desktop: CSP `img-src` harus mengizinkan origin `R2_PUBLIC_URL` |
| `timezone` | string | Zona IANA, lihat [Timezone](#timezone) |
| `has_password` | bool | `false` = akun hanya bisa sign in lewat Google. FE memakainya untuk memilih cara [re-autentikasi](account.md#re-autentikasi) dan untuk menampilkan "buat password" (lewat [`/forgot-password`](#11-forgot-password)) alih-alih "ganti password" |
| `is_email_verified` | bool | Kepemilikan email sudah terbukti (kode verifikasi, reset password, Google, atau admin) |
| `google` | object \| null | Akun Google yang ter-link: `email` (email akun Google, bisa `null`) dan `linked_at`. `null` kalau tidak ada. Satu akun Timebox paling banyak satu akun Google |

> User object di [`/users`](user.md#user-object) bentuknya berbeda (field kosong tidak dikirim, ada `is_active`, `role_name`, dst.).

---

## Timezone

Setiap user punya `timezone`: nama zona IANA, mis. `Asia/Jakarta`, `Asia/Makassar`, `America/New_York`, `UTC`. Default `Asia/Jakarta` — juga untuk akun yang sudah ada sebelum field ini.

- **Semua timestamp di API tetap UTC** (RFC 3339, akhiran `Z`). `timezone` hanya memberi tahu FE dan kode fitur cara menampilkan dan mengelompokkan tanggal (mis. "hari ini" milik user).
- Diisi saat [signup](#2-sign-up) dan saat [Google sign-in](#4-sign-in-with-google) membuat akun baru (opsional), diubah lewat [`PUT /users/me`](user.md#7-update-profile-me) atau oleh admin lewat [`PUT /users/:id`](user.md#4-update-user). Dikirim sebagai `timezone` di [objek `user`](#user-object) dan di user object [`/users`](user.md#user-object).
- **Validasi:** harus nama zona IANA, max 64 karakter. `Local`, singkatan (`WIB`), offset (`+07:00`), nama yang tidak dikenal, dan string kosong → `400 validation_failed` dengan `errors.timezone: ["is invalid (timezone)"]`. Tidak dikirim atau `null` → default (di endpoint update: tidak diubah).
- **FE:** kirim zona browser / OS saat signup / Google sign-in pertama: `Intl.DateTimeFormat().resolvedOptions().timeZone`.

---

## Klien Web vs Desktop

Refresh token dikirim dengan dua cara, dipilih per request oleh header **`X-Timebox-Client`**:

| | Aplikasi desktop (Electron main process) | Aplikasi web (browser) |
|---|---|---|
| Header | Tidak dikirim | `X-Timebox-Client: web` |
| Refresh token di response | `data.refresh_token` | Cookie `tb_refresh`; `refresh_token` **tidak ada** di body |
| Refresh token di `/refresh`, `/logout` | Body `{"refresh_token": "..."}` | Cookie (body kosong). Boleh juga body |
| Penyimpanan | OS keychain (mis. Electron `safeStorage`) | Browser, tidak terjangkau JavaScript |

**Cookie `tb_refresh`:** `HttpOnly`, `Path=/core/v1/auth`, `SameSite=Strict`, `Secure` (kecuali `ENV=development`, supaya `http://localhost` bisa), `Max-Age` = `JWT_REFRESH_EXPIRATION` (default 7 hari). Dipasang oleh setiap endpoint yang membuat sesi — [`/signin`](#1-sign-in), [`/google`](#4-sign-in-with-google), [`/verify-email`](#9-verify-email), [`/reset-password`](#13-reset-password) — dan diganti di setiap [`/refresh`](#5-refresh-token).

- **Refresh lewat cookie** butuh header `X-Timebox-Client: web` **dan** header `Origin` yang ada di `CORS_ALLOWED_ORIGINS`. Selain itu → `403 origin_not_allowed`. Origin yang tidak ada di daftar sudah ditolak lebih awal oleh CORS: `403` dengan body kosong (bukan envelope JSON).
- **Cookie dihapus** (`Max-Age=-1`) oleh `/logout`, `/logout-all`, [`POST /account/delete`](account.md#9-delete-account), dan oleh `/refresh` yang gagal karena sesinya berakhir (`session_ended`, `user_inactive`, `email_not_verified`) — hanya kalau request membawa `X-Timebox-Client: web`.
- **Same-site:** `SameSite=Strict` membuat cookie hanya terkirim kalau API dan aplikasi web satu *site* (domain terdaftar yang sama), mis. `app.timebox.id` ↔ `api.timebox.id`. Di production keduanya **wajib** satu site; kalau tidak, browser tidak mengirim cookie dan `/refresh` menjawab `401 session_ended`.
- **FE web:** kirim `credentials: 'include'` (fetch) / `withCredentials: true` (axios) di request `/auth/*` supaya cookie disimpan dan dikirim. Access token tetap di memory dan dikirim di header `Authorization`.
- **Desktop:** request dari main process Electron tidak membawa `Origin`, jadi tidak bisa memakai cookie — tetap body.

---

## Kode di Email (OTP)

Verifikasi email, reset password, dan ganti email membuktikan kepemilikan mailbox dengan kode 6 digit yang dikirim lewat email:

| Tujuan | Diminta lewat | Dikirim ke | Ditukar di |
|---|---|---|---|
| Verifikasi email | [`/signup`](#2-sign-up), [`/verify-email/resend`](#10-resend-verification-code) | Alamat akun | [`/verify-email`](#9-verify-email) → sesi |
| Reset password | [`/forgot-password`](#11-forgot-password) | Alamat akun | [`/reset-password/verify`](#12-verify-reset-code) → `reset_token` |
| Ganti email | [`POST /account/email`](account.md#3-request-email-change), [`/account/email/resend`](account.md#4-resend-email-change-code) | Alamat **baru** | [`/account/email/confirm`](account.md#5-confirm-email-change) |

- **Bentuk:** 6 digit angka, bisa diawali `0` — kirim sebagai string (`"042917"`). Kode juga ada di subject email.
- **Masa berlaku:** `OTP_EXPIRATION` (default 15 menit); `expires_in` di response menyebutnya dalam detik. Sekali pakai.
- **Tebakan salah:** `400 invalid_code` dengan `data.attempts_left` — termasuk kode lama yang sudah diganti kode baru: yang dicek selalu kode terbaru, jadi kode lama dihitung sebagai satu tebakan salah. Tebakan yang mencapai `OTP_MAX_ATTEMPTS` (default 5) mematikan kode: tebakan itu dan setiap percobaan sesudahnya → `429 too_many_attempts`, `retry_after` = detik sampai kode baru boleh diminta (minimal 1). Minta kode baru.
- **Hanya kode terbaru yang berlaku:** kode baru me-retire kode lama untuk tujuan yang sama; memasukkan kode lama → `invalid_code`.
- **Jeda kirim ulang:** `OTP_RESEND_COOLDOWN` (default 60 detik) per alamat dan per tujuan (verifikasi / reset), **juga untuk alamat yang tidak terdaftar**, jadi jawabannya tidak membocorkan apakah akun ada. Terlalu cepat → `429 resend_cooldown` dengan `retry_after`. Response yang mengirim kode membawa `resend_available_in` (detik) untuk countdown tombol "kirim ulang". Ganti email: jeda per akun. Signup selalu mengirim kode pertama walaupun jeda alamat itu sedang berjalan.
- **Kuota per akun per jam:** paling banyak `MAX_VERIFICATION_EMAILS` (default 5) kode verifikasi — termasuk dari signup — dan `MAX_PASSWORD_RESET_EMAILS` (default 3) kode reset. Lewat dari itu endpoint tetap menjawab sama tapi tidak mengirim apa pun; kode terakhir yang terkirim tetap berlaku. Kode ganti email punya hitungan sendiri dengan batas `MAX_VERIFICATION_EMAILS`; kelebihannya dijawab `429 email_quota_exceeded`.
- **Terikat ke alamat akun:** kalau email akun berubah setelah kode dikirim, kode itu batal (`code_expired`).
- **Dibatalkan** saat password diganti atau direset dan saat akun diklaim: semua kode yang belum dipakai mati.
- **Tidak ada kode yang bisa dicek** — kode terbaru kedaluwarsa atau sudah dipakai, semua kode dibatalkan (ganti / reset password, klaim akun), tidak pernah ada kode yang dikirim, email tidak dikenal **atau milik akun nonaktif** (jawabannya sama, supaya tidak membocorkan akun mana yang ada), atau alamat akun sudah berubah → `400 code_expired` (`This code has expired`, detail `Ask for a new code`).
- **Disimpan sebagai HMAC-SHA256** dengan kunci turunan `JWT_SECRET` — mengganti `JWT_SECRET` membatalkan semua kode yang tertunda.
- **Email dikirim di background:** response tidak menunggu SMTP, dan lamanya sama entah akun itu ada atau tidak. Tanpa SMTP (development) kode hanya ditulis di log level debug.

---

## Endpoints

### 1. Sign In

Autentikasi dengan email/username dan password.

```
POST /core/v1/auth/signin
```

**Auth:** Tidak diperlukan (rate limit per IP). Web: kirim `X-Timebox-Client: web` ([mode cookie](#klien-web-vs-desktop)).

**Request:**
```json
{
  "login": "rina@timebox.id",
  "password": "Kalender-Rapi-7"
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `login` | string | Ya | Email atau username, max 254. Tidak case-sensitive. Mengandung `@` → dicocokkan ke email; selain itu ke username |
| `password` | string | Ya | Max 72 karakter |

**Response (200):**
```json
{
  "data": {
    "access_token": "eyJhbGci...",
    "refresh_token": "9f86d081884c7d65...",
    "token_type": "Bearer",
    "expires_in": 900,
    "user": {
      "id": "550e8400-...",
      "email": "rina@timebox.id",
      "username": "rina",
      "full_name": "Rina Wijaya",
      "avatar_url": null,
      "timezone": "Asia/Jakarta",
      "has_password": true,
      "is_email_verified": true,
      "google": null
    },
    "roles": ["user_manager"],
    "permissions": ["users:create", "users:read", "users:update", "users:delete", "roles:read"]
  },
  "message": "Sign in successful"
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `access_token` | string | JWT. Kirim di header `Authorization: Bearer <token>` |
| `refresh_token` | string | Token opaque (64 karakter hex). **Tidak ada** untuk klien web — ada di cookie `tb_refresh` |
| `token_type` | string | Selalu `"Bearer"` |
| `expires_in` | int | Umur access token dalam detik — mengikuti `JWT_EXPIRATION` (default 900) |
| `user` | object | [User object](#user-object) |
| `roles` | string[] | Kode role aktif user |
| `permissions` | string[] | Flat permission list untuk gating UI |

Response yang sama (sesi baru) dikembalikan oleh `/google`, `/verify-email`, dan `/reset-password`.

> User tanpa role (mis. baru signup) tetap bisa sign in, dengan `"roles": []` dan `"permissions": []`.

**Lockout:** setiap password salah — di sign in, di [ganti password](user.md#8-change-password), di [re-autentikasi](account.md#re-autentikasi) (`POST /account/email`, `POST /account/delete`), maupun di [link akun Google](account.md#6-link-google-account) — menambah hitungan yang sama; setelah `MAX_LOGIN_ATTEMPTS` (default 5) berturut-turut akun dikunci selama `ACCOUNT_LOCKOUT_DURATION` (default 30 menit). Selama terkunci, password yang benar pun dijawab sama seperti password salah. Password yang benar me-reset hitungan, termasuk saat sign in kemudian ditolak karena akun tidak aktif atau email belum terverifikasi; setelah lock berakhir hitungan mulai dari nol. Percobaan dihitung **sebelum** password dicek, jadi tebakan yang dikirim paralel pun tidak lebih dari `MAX_LOGIN_ATTEMPTS` yang sempat dicek. Saat lock terpasang, pemilik akun dikirimi email (sekali per lock, menyebut lama lock-nya) — response sign in sendiri tetap tidak menyebut lock. [Reset password](#13-reset-password) membuka lock seketika.

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | Validasi gagal (`errors` per field) |
| 401 | `invalid_credentials` | Invalid credentials. After several failed attempts, sign-in is paused for a while. | Login tidak dikenal, password salah, **atau** akun sedang terkunci |
| 401 | `user_inactive` | User account is not active | Password benar, tapi user dinonaktifkan |
| 403 | `email_not_verified` | Email not verified — detail `Enter the code sent to your email, or ask for a new one` | Password benar, email belum terverifikasi, dan `EMAIL_VERIFICATION_REQUIRED=true` (default `false`). `data: {"email": "rina@timebox.id"}` — alamat akun, juga kalau user sign in dengan username. FE: tawarkan [kirim kode](#10-resend-verification-code) lalu [verifikasi](#9-verify-email) |
| 429 | `rate_limited` | Too many requests. Please try again later. | Rate limit per IP; `retry_after` / header `Retry-After` |

---

### 2. Sign Up

Registrasi user baru. User dibuat **tanpa role** — belum punya akses apa pun sampai ada yang meng-assign role (lihat [user.md](user.md#4-update-user)).

```
POST /core/v1/auth/signup
```

**Auth:** Tidak diperlukan (rate limit per IP)

**Request:**
```json
{
  "email": "rina@timebox.id",
  "username": "rina",
  "password": "Kalender-Rapi-7",
  "full_name": "Rina Wijaya",
  "phone": "+6281234567890",
  "timezone": "Asia/Jakarta"
}
```

| Field | Type | Required | Validasi |
|-------|------|----------|----------|
| `email` | string | Ya | Format email, max 254. Disimpan lowercase |
| `username` | string | Tidak | 3–50 karakter: huruf, angka, `.`, `_`, `-`; diawali huruf/angka (jadi tanpa `@`). Disimpan lowercase. Tidak dikirim / `""` → diturunkan dari bagian email sebelum `@` (karakter tidak valid dibuang; kalau sudah dipakai ditambah `-` + 8 karakter hex acak), sama seperti akun Google |
| `password` | string | Ya | 8–72 byte, minimal satu huruf besar, satu huruf kecil, dan satu angka; bukan password umum (dicek tanpa membedakan huruf besar/kecil — mis. `Password1`, `Welcome123` ditolak) |
| `full_name` | string | Tidak | Max 255 karakter |
| `phone` | string | Tidak | Max 20 karakter |
| `timezone` | string | Tidak | Nama zona IANA, mis. `Asia/Jakarta`. Tidak dikirim → `Asia/Jakarta`. FE: `Intl.DateTimeFormat().resolvedOptions().timeZone`. Lihat [Timezone](#timezone) |

**Response (201):**
```json
{
  "data": {
    "user": {
      "id": "550e8400-...",
      "email": "rina@timebox.id",
      "username": "rina",
      "full_name": "Rina Wijaya",
      "avatar_url": null,
      "timezone": "Asia/Jakarta",
      "has_password": true,
      "is_email_verified": false,
      "google": null
    },
    "verification": {
      "email": "rina@timebox.id",
      "expires_in": 900,
      "resend_available_in": 60
    }
  },
  "message": "Account created. Enter the code sent to your email."
}
```

| Field | Keterangan |
|-------|------------|
| `user` | [User object](#user-object) akun baru |
| `verification.email` | Alamat tujuan kode verifikasi |
| `verification.expires_in` | Umur kode dalam detik (`OTP_EXPIRATION`) |
| `verification.resend_available_in` | Detik sampai [kirim ulang](#10-resend-verification-code) boleh dipakai |

> **Tidak ada token.** User dibuat dengan `is_active=true` dan `is_email_verified=false` — tidak ada yang membuktikan email itu milik pendaftar. Signup mengirim [kode verifikasi](#kode-di-email-otp); email yang gagal terkirim tidak menggagalkan signup (minta ulang lewat [`/verify-email/resend`](#10-resend-verification-code)). Lanjutkan dengan [`POST /verify-email`](#9-verify-email) `{email, code}`, yang sekaligus sign in. Selama `EMAIL_VERIFICATION_REQUIRED=false` (default) user juga bisa langsung [sign in](#1-sign-in) dengan password sebelum verifikasi.
>
> Selama emailnya belum terverifikasi, akun ini bisa [diklaim](#klaim-akun-email-belum-terverifikasi) oleh pemilik asli alamat itu.

**Errors:**

| Status | `code` | Message |
|--------|--------|---------|
| 400 | `validation_failed` | Invalid request payload (`errors` per field) |
| 409 | `email_taken` | Email already exists |
| 409 | `username_taken` | Username already exists (hanya untuk `username` yang dipilih sendiri) |
| 429 | `rate_limited` | Too many requests. Please try again later. |

---

### 3. Get Google Client

OAuth client id yang dipakai aplikasi untuk memulai Google sign-in, per platform.

```
GET /core/v1/auth/google/client?platform=desktop
```

**Auth:** Tidak diperlukan (rate limit per IP)

| Query | Required | Keterangan |
|-------|----------|------------|
| `platform` | Ya | `desktop` (aplikasi Electron, client Google tipe "Desktop app") atau `web` (browser, client tipe "Web application") |

**Response (200):**
```json
{
  "data": {
    "platform": "web",
    "client_id": "1234567890-abc123.apps.googleusercontent.com",
    "redirect_uris": ["https://app.timebox.id/oauth/google"]
  },
  "message": "Google client retrieved"
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `platform` | string | Sama dengan query |
| `client_id` | string | Client id publik untuk URL otorisasi Google |
| `redirect_uris` | string[] \| null | `web`: redirect URI yang diizinkan (`GOOGLE_WEB_REDIRECT_URIS`) — pakai salah satu persis. `desktop`: `null` — redirect ke port loopback pilihan aplikasi (lihat [Sign In with Google](#4-sign-in-with-google)) |

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid query parameters | `platform` tidak ada atau bukan `desktop` / `web` |
| 429 | `rate_limited` | Too many requests. Please try again later. | Rate limit per IP |
| 503 | `google_not_configured` | Google sign-in is not configured | Platform itu belum punya OAuth client di BE (`GOOGLE_DESKTOP_*` / `GOOGLE_WEB_*` kosong). FE: sembunyikan / nonaktifkan tombol Google |

---

### 4. Sign In with Google

Sign in **atau** sign up dengan akun Google. Aplikasi menjalankan **authorization code flow + PKCE** ke Google, lalu mengirim `code`-nya ke sini; BE menukarnya ke Google dengan client secret dan memverifikasi ID token-nya. Satu endpoint, tiga skenario yang dibedakan lewat `is_new_user`.

```
POST /core/v1/auth/google
```

**Auth:** Tidak diperlukan (rate limit per IP). Web: kirim `X-Timebox-Client: web` ([mode cookie](#klien-web-vs-desktop)).

#### Alur di aplikasi

1. `GET /auth/google/client?platform=…` → `client_id` (dan `redirect_uris` untuk web).
2. Buat `code_verifier` acak (43–128 karakter dari `A-Z a-z 0-9 - . _ ~`), `code_challenge` = BASE64URL(SHA-256(`code_verifier`)) tanpa padding, dan `state` acak.
3. Tentukan `redirect_uri`:
   - **desktop:** jalankan server HTTP sementara di loopback, port acak: `http://127.0.0.1:<port>/<path>` (atau `http://[::1]:<port>/<path>`). Port wajib; `localhost` tidak diterima; tanpa `#fragment`.
   - **web:** salah satu `redirect_uris` dari langkah 1, **persis sama** (mis. `https://app.timebox.id/oauth/google`). Simpan `code_verifier` dan `state` di `sessionStorage`.
4. Buka (desktop: di browser sistem) `https://accounts.google.com/o/oauth2/v2/auth` dengan `client_id`, `redirect_uri`, `response_type=code`, `scope=openid email profile`, `code_challenge`, `code_challenge_method=S256`, `state` (opsional `prompt=select_account`).
5. Google me-redirect ke `redirect_uri?code=…&state=…`. Cocokkan `state`, lalu **segera** kirim `code` ke endpoint ini — code Google sekali pakai dan cepat kedaluwarsa.

```js
// web — halaman /oauth/google
const params = new URLSearchParams(location.search)
if (params.get('state') !== sessionStorage.getItem('google_state')) throw new Error('state mismatch')

const res = await fetch(`${API}/core/v1/auth/google`, {
  method: 'POST',
  credentials: 'include',
  headers: { 'Content-Type': 'application/json', 'X-Timebox-Client': 'web' },
  body: JSON.stringify({
    code: params.get('code'),
    code_verifier: sessionStorage.getItem('google_code_verifier'),
    redirect_uri: 'https://app.timebox.id/oauth/google', // sama persis dengan langkah 4
    platform: 'web',
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone, // dipakai kalau akun baru dibuat
  }),
})
```

#### Request

```json
{
  "code": "4/0AeanS0bX...",
  "code_verifier": "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
  "redirect_uri": "http://127.0.0.1:53682/oauth/google",
  "platform": "desktop",
  "timezone": "Asia/Jakarta"
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `code` | string | Ya | Authorization code dari redirect Google, max 2048 |
| `code_verifier` | string | Ya | PKCE verifier asli (bukan challenge-nya), 43–128 karakter |
| `redirect_uri` | string | Ya | Redirect URI yang dipakai di langkah 4, persis sama. Max 2048 |
| `platform` | string | Ya | `desktop` atau `web` — menentukan OAuth client dan aturan `redirect_uri` |
| `timezone` | string | Tidak | Nama zona IANA untuk akun **baru** — hanya dipakai kalau request ini membuat akun (`is_new_user: true`); akun yang sudah ada tetap memakai zonanya. Tidak dikirim → `Asia/Jakarta`. Lihat [Timezone](#timezone) |

#### Response (200 — returning user) atau (201 — akun baru)

```json
{
  "data": {
    "access_token": "eyJhbGci...",
    "refresh_token": "9f86d081884c7d65...",
    "token_type": "Bearer",
    "expires_in": 900,
    "is_new_user": true,
    "user": {
      "id": "550e8400-...",
      "email": "rina.wijaya@gmail.com",
      "username": "rina.wijaya",
      "full_name": "Rina Wijaya",
      "avatar_url": "https://cdn.timebox.id/avatars/550e8400-.../9c1e3f2b-....jpg",
      "timezone": "Asia/Jakarta",
      "has_password": false,
      "is_email_verified": true,
      "google": { "email": "rina.wijaya@gmail.com", "linked_at": "2026-10-24T03:00:00Z" }
    },
    "roles": [],
    "permissions": []
  },
  "message": "Account created and signed in with Google"
}
```

Field sama dengan [`/signin`](#1-sign-in) (`refresh_token` tidak ada untuk klien web), ditambah:

| Field | Type | Keterangan |
|-------|------|------------|
| `is_new_user` | bool | `true` → request ini membuat akun baru (tanpa role), HTTP `201`, message `Account created and signed in with Google`. `false` → user sudah ada, HTTP `200`, message `Sign in with Google successful` |

Untuk akun baru: `username` diturunkan dari bagian email sebelum `@` (karakter tidak valid dibuang; kalau sudah dipakai ditambah `-` + 8 karakter hex acak), `full_name` dari nama Google, `timezone` dari request (default `Asia/Jakarta`), email langsung terverifikasi, dan email welcome dikirim.

**Foto profil:** foto akun Google **disalin** ke R2 (JPEG ≤512 px) — untuk akun baru, dan saat akun Google di-link ke akun yang belum punya foto. `avatar_url` tidak pernah berisi URL Google (`lh3.googleusercontent.com`). Kalau R2 / `R2_PUBLIC_URL` tidak dikonfigurasi atau penyalinan gagal (batas 5 detik), sign in tetap berhasil tanpa foto. Foto tidak diperbarui di sign in berikutnya.

#### Skenario

Identity dicari berdasarkan **id akun Google** (`sub`), bukan email — tetap cocok walau email akun Google berganti.

| Kondisi | `is_new_user` | Hasil |
|---|---|---|
| Akun Google sudah ter-link ke akun Timebox | `false` | User itu sign in — dengan `EMAIL_VERIFICATION_REQUIRED=true` hanya kalau email akunnya terverifikasi, selain itu `403 email_not_verified` |
| Belum ter-link, ada user dengan email sama **dan** `is_email_verified=true` | `false` | Akun Google di-link ke user itu, lalu sign in. Role user tidak berubah, tidak ada yang di-revoke. Alamat akun dikirimi email "akun Google ditambahkan" |
| Belum ter-link, ada user dengan email sama tapi **belum terverifikasi** (mis. hasil signup) | `false` | **Akun diklaim** oleh pemilik email: akun Google di-link, password akun dihapus, email jadi terverifikasi, lock dibuka, semua sesi, semua API key, dan semua kode email yang belum dipakai di-revoke — lalu caller sign in. Role tidak berubah. Lihat [Klaim akun](#klaim-akun-email-belum-terverifikasi) |
| Belum ter-link, user dengan email sama sudah me-link akun Google **lain** | - | `409 google_account_conflict` |
| Belum ada user sama sekali | `true` | User baru + identity dibuat dalam satu transaksi. **Tanpa role**, tanpa password |

> Skenario selain yang pertama mensyaratkan Google sudah memverifikasi emailnya (`email_verified=true`); kalau belum → `401 google_email_not_verified`.
>
> Satu akun Timebox paling banyak me-link satu akun Google, dan satu akun Google hanya ke satu akun Timebox. Link / unlink dari pengaturan akun: [account.md](account.md#6-link-google-account).
>
> Google sign-in tidak memakai password, jadi **tidak terhalang lock** akibat password salah dan **tidak me-reset** hitungannya — kecuali saat klaim, yang membuka lock.
>
> User Google-only (tanpa password) bisa membuat password lewat [`/forgot-password`](#11-forgot-password).

#### Errors

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | Field wajib tidak ada (termasuk request lama dengan `id_token`), `code_verifier` di luar 43–128, `platform` bukan `desktop`/`web`, atau `timezone` tidak valid |
| 400 | `invalid_redirect_uri` | Redirect URI is not allowed | desktop: bukan `http://127.0.0.1:<port>/…` / `http://[::1]:<port>/…`; web: tidak persis sama dengan salah satu `GOOGLE_WEB_REDIRECT_URIS` |
| 401 | `google_auth_failed` | Google sign-in failed — detail `Try signing in with Google again` | Google menolak code (sudah dipakai, kedaluwarsa, `code_verifier` / `redirect_uri` tidak cocok dengan yang dipakai saat otorisasi, client salah), Google tidak terjangkau, ID token tidak valid, atau akun Google tanpa email (scope `email` tidak diminta) |
| 401 | `google_email_not_verified` | Google account email is not verified | Google belum memverifikasi email akun itu dan akun Google ini belum ter-link |
| 401 | `user_inactive` | User account is not active | User yang ter-link / cocok emailnya dinonaktifkan |
| 403 | `email_not_verified` | Email not verified — detail `Enter the code sent to your email, or ask for a new one` | `EMAIL_VERIFICATION_REQUIRED=true` dan email akun yang ter-link belum / tidak lagi terverifikasi (mis. dicabut admin). `data: {"email": "..."}` |
| 409 | `google_account_conflict` | The account with this email signs in with another Google account — detail `Sign in with that Google account or with your password` | Akun dengan email ini sudah me-link akun Google lain |
| 429 | `rate_limited` | Too many requests. Please try again later. | Rate limit per IP |
| 503 | `google_not_configured` | Google sign-in is not configured | BE tidak punya OAuth client untuk `platform` itu. FE: tampilkan "Login Google sementara tidak tersedia" |

---

### 5. Refresh Token

Tukar refresh token dengan pasangan token baru.

```
POST /core/v1/auth/refresh
```

**Auth:** Tidak diperlukan (rate limit per IP)

**Request — desktop:**
```json
{
  "refresh_token": "9f86d081884c7d65..."
}
```

**Request — web:** body kosong (atau `{}`), header `X-Timebox-Client: web`, `credentials: 'include'`. Refresh token dibaca dari cookie `tb_refresh`; `Origin` harus ada di `CORS_ALLOWED_ORIGINS` (lihat [Klien web vs desktop](#klien-web-vs-desktop)).

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `refresh_token` | string | Tidak | Max 128. Kalau ada, dipakai (juga oleh klien web); kalau tidak, cookie |

**Response (200):**
```json
{
  "data": {
    "access_token": "eyJhbGci...",
    "refresh_token": "1b4f0e9851971998...",
    "token_type": "Bearer",
    "expires_in": 900
  },
  "message": "Token refreshed successfully"
}
```

> Klien web: `refresh_token` tidak ada di body; cookie `tb_refresh` diganti dengan token baru.
>
> **Rotation:** setiap refresh menghasilkan refresh token **baru**; token lama di-revoke di transaksi yang sama. Dari beberapa request bersamaan dengan token yang sama hanya satu yang berhasil — FE dengan banyak tab sebaiknya menjalankan refresh di satu tempat dan membagikan hasilnya. Pasangan token baru tetap di **sesi yang sama**: `sid` di access token, dan device, IP, serta waktu sign in sesi itu di [`GET /account/sessions`](account.md#1-list-sessions), tidak berubah.
>
> **Status akun dicek ulang:** user nonaktif / dihapus → `401 user_inactive`; dengan `EMAIL_VERIFICATION_REQUIRED=true`, email yang belum terverifikasi (mis. verifikasinya dicabut admin setelah sign in) → `403 email_not_verified`. Di kedua kasus refresh token yang dikirim tetap terpakai dan sesi itu berakhir — access token-nya ikut ditolak sejak request berikutnya; mengirim refresh token itu lagi → `401 session_ended`. User perlu sign in lagi (setelah emailnya terverifikasi).
>
> **Reuse detection:** token yang sudah dirotasi lalu dipakai lagi **lebih dari 30 detik** kemudian dianggap bocor → `401 session_ended` (`Refresh token revoked`) **dan semua sesi user diakhiri** (semua refresh token di-revoke, semua access token ditolak). Dalam 30 detik (mis. dua tab bersamaan) hanya ditolak `401`. Token yang di-revoke lewat logout tidak memicu ini.
>
> **Gagal di tengah jalan** (`500`, mis. database sesaat tidak bisa dihubungi): rotasi dibatalkan dan refresh token yang dikirim tetap berlaku — ulangi dengan token yang sama.
>
> Umur refresh token: `JWT_REFRESH_EXPIRATION` (default 168 jam = 7 hari), dihitung ulang dari setiap refresh. `roles`/`permissions` tidak dikirim di sini — permission tidak berubah karena refresh.

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | `refresh_token` lebih dari 128 karakter |
| 401 | `session_ended` | Invalid refresh token / Refresh token expired / Refresh token revoked — detail `Sign in again` | Token tidak dikenal, kedaluwarsa, atau sudah di-revoke (logout, rotasi, sesi diakhiri, semua sesi diakhiri) |
| 401 | `session_ended` | No refresh token — detail `Sign in again` | Klien web tanpa cookie `tb_refresh` (kedaluwarsa, dihapus, atau tidak terkirim karena API dan app beda site) |
| 401 | `user_inactive` | User account is not active | User dinonaktifkan / dihapus |
| 403 | `email_not_verified` | Email not verified | `EMAIL_VERIFICATION_REQUIRED=true` dan email user belum (atau tidak lagi) terverifikasi. Tanpa `data` |
| 403 | `origin_not_allowed` | Refresh not allowed — detail `Send refresh_token, or the cookie from the web app on an allowed origin` | Body tanpa `refresh_token`, dan request tidak membawa `X-Timebox-Client: web` atau `Origin` yang ada di `CORS_ALLOWED_ORIGINS` |
| 403 | (body kosong) | - | `Origin` di luar `CORS_ALLOWED_ORIGINS` — ditolak middleware CORS sebelum sampai ke endpoint |
| 429 | `rate_limited` | Too many requests. Please try again later. | Rate limit per IP |

> Klien web: `session_ended`, `user_inactive`, dan `email_not_verified` di sini juga menghapus cookie `tb_refresh`.

---

### 6. Logout (Single Device)

Akhiri satu sesi caller.

```
POST /core/v1/auth/logout
```

**Auth:** `Authorization: Bearer <access_token>` (API key → `403`). Web: kirim `X-Timebox-Client: web` supaya cookie dihapus.

**Request** (opsional):
```json
{
  "refresh_token": "9f86d081884c7d65..."
}
```

Sesi yang diakhiri, menurut urutan:

1. `refresh_token` di body → sesi milik token itu;
2. selain itu cookie `tb_refresh` → sesi milik token di cookie;
3. selain itu → **sesi access token ini** (`sid`).

**Response (200):**
```json
{
  "data": null,
  "message": "Logged out successfully"
}
```

> Refresh token yang bukan milik caller, tidak dikenal, atau sudah di-revoke dibiarkan — response tetap `200`.
>
> **Access token sesi itu ikut mati:** refresh token di-revoke, dan mulai request berikutnya access token dengan `sid` sesi itu → `401 session_ended`. Sesi lain user ini tidak terpengaruh. FE: buang kedua token. Dengan `X-Timebox-Client: web` cookie `tb_refresh` dihapus.
>
> Mengakhiri sesi device lain tanpa refresh token-nya: [`DELETE /account/sessions/:id`](account.md#2-end-session). Semua sesi sekaligus: `/logout-all`.

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | `refresh_token` lebih dari 128 karakter |
| 401 | `unauthorized` / `token_expired` / `session_ended` / `user_inactive` | Unauthorized | Lihat [authentication errors](../README.md#authentication-errors) |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` | Dipanggil dengan API key |

---

### 7. Logout All Devices

Akhiri semua sesi user.

```
POST /core/v1/auth/logout-all
```

**Auth:** `Authorization: Bearer <access_token>` (API key → `403`). Web: kirim `X-Timebox-Client: web` supaya cookie dihapus.

**Request:** Body kosong

**Response (200):**
```json
{
  "data": null,
  "message": "Logged out from all devices successfully"
}
```

> Semua refresh token user di-revoke **dan** semua access token yang terbit sampai saat ini ditolak — termasuk token yang dipakai untuk request ini. Request berikutnya dengan token lama → `401 session_ended`.
>
> API key milik user **tidak** ikut di-revoke; revoke lewat [`DELETE /api-keys/:id`](api-key.md#5-revoke-api-key).

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 401 | `unauthorized` / `token_expired` / `session_ended` / `user_inactive` | Unauthorized | Lihat [authentication errors](../README.md#authentication-errors) |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` | Dipanggil dengan API key |

---

### 8. Get Me (Profile + Effective Permissions)

Profil user yang sedang login beserta role dan **permission efektif saat ini**.

```
GET /core/v1/auth/me
```

**Auth:** `Authorization: Bearer <access_token>` (API key → `403`)

**Response (200):**
```json
{
  "data": {
    "user": {
      "id": "550e8400-...",
      "email": "rina@timebox.id",
      "username": "rina",
      "full_name": "Rina Wijaya",
      "avatar_url": "https://cdn.timebox.id/avatars/550e8400-.../3f2b9c1e-....jpg",
      "timezone": "Asia/Jakarta",
      "has_password": true,
      "is_email_verified": true,
      "google": { "email": "rina.wijaya@gmail.com", "linked_at": "2026-10-21T09:00:00Z" }
    },
    "roles": ["user_manager"],
    "permissions": ["users:create", "users:read", "users:update", "users:delete", "roles:read"],
    "is_super_admin": false
  },
  "message": "Profile retrieved successfully"
}
```

| Field | Keterangan |
|-------|------------|
| `user` | [User object](#user-object) dari database (bukan dari JWT) — email, foto, `timezone`, dan akun Google terbaru |
| `roles` | Kode role aktif user saat ini |
| `permissions` | Permission efektif saat ini (cache authz, fallback ke DB) — bukan isi JWT |
| `is_super_admin` | `true` selama user memegang role `super_admin` yang aktif; backend melewatkan semua cek permission untuknya |

**Kapan FE memanggil endpoint ini:**

1. **Setelah page reload** — re-hydrate permissions tanpa menyimpannya di localStorage (web: `POST /refresh` dulu lewat cookie untuk mendapat access token).
2. **Setelah 403 tak terduga** — cek apakah permissions berubah (role dicabut admin). Kalau berubah, update state; kalau sama, user memang tidak punya akses.

| | `/signin` | `/me` |
|---|---|---|
| Mengembalikan token? | ✅ | ❌ |
| Mengubah state server? | ✅ (sesi baru) | ❌ read-only |
| Bisa dipanggil berulang? | ❌ (butuh password, kena rate limit) | ✅ |

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 401 | `unauthorized` / `token_expired` / `session_ended` / `user_inactive` | Unauthorized | Token tidak ada / tidak valid / expired / sesi diakhiri / user tidak aktif — lihat [authentication errors](../README.md#authentication-errors) |
| 403 | `forbidden` | Forbidden — `This endpoint cannot be used with an API key` | Dipanggil dengan API key |

---

### 9. Verify Email

Verifikasi email dengan kode yang dikirim saat signup (atau lewat kirim ulang), **dan sign in**: kode membuktikan kepemilikan mailbox seperti password membuktikan akun.

```
POST /core/v1/auth/verify-email
```

**Auth:** Tidak diperlukan (rate limit per IP). Web: kirim `X-Timebox-Client: web` ([mode cookie](#klien-web-vs-desktop)).

**Request:**
```json
{
  "email": "rina@timebox.id",
  "code": "042917"
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `email` | string | Ya | Format email, max 254. Tidak case-sensitive |
| `code` | string | Ya | Tepat 6 digit angka |

**Response (200):** sesi baru, sama dengan [`/signin`](#1-sign-in) — message `Email verified`.

> Email akun jadi `is_email_verified=true`. Verifikasi pertama mengirim email welcome. Sign in ini berlaku walau `EMAIL_VERIFICATION_REQUIRED=true`, tidak terhalang lock password, dan tidak me-reset hitungannya. Aturan kode: [Kode di email](#kode-di-email-otp).

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | `email` / `code` tidak ada atau formatnya salah (`code` harus 6 digit) |
| 400 | `invalid_code` | Invalid code | Kode salah, atau kode lama yang sudah diganti kode baru (dihitung sebagai tebakan salah); `data: {"attempts_left": 4}` |
| 400 | `code_expired` | This code has expired — detail `Ask for a new code` | Tidak ada kode yang bisa dicek: email tidak dikenal atau milik akun nonaktif, kode terbaru kedaluwarsa / sudah dipakai, kode dibatalkan, atau alamat akun berubah |
| 429 | `too_many_attempts` | Too many wrong codes — detail `Ask for a new code` | Kode mati karena `OTP_MAX_ATTEMPTS` tebakan salah; `retry_after` = detik sampai [kirim ulang](#10-resend-verification-code) boleh dipakai |
| 429 | `rate_limited` | Too many requests. Please try again later. | Rate limit per IP |

---

### 10. Resend Verification Code

Kirim kode verifikasi baru.

```
POST /core/v1/auth/verify-email/resend
```

**Auth:** Tidak diperlukan (rate limit per IP)

**Request:**
```json
{
  "email": "rina@timebox.id"
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `email` | string | Ya | Format email, max 254. Tidak case-sensitive |

**Response (200)** — selalu sama, apa pun alamatnya:
```json
{
  "data": {
    "expires_in": 900,
    "resend_available_in": 60
  },
  "message": "If an unverified account uses this email, a verification code has been sent to it."
}
```

> Kode hanya dikirim ke akun **aktif** yang emailnya **belum terverifikasi**. Email tidak dikenal, sudah terverifikasi, akun nonaktif, atau kuota per jam habis → jawaban sama, tidak ada email. Kode baru membatalkan kode verifikasi sebelumnya. Jeda kirim ulang berjalan per alamat untuk semua alamat itu.

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | `email` tidak ada / format salah |
| 429 | `resend_cooldown` | A code was sent moments ago — detail `Wait before asking for another` | Kode untuk alamat ini diminta kurang dari `OTP_RESEND_COOLDOWN` lalu; `retry_after` |
| 429 | `rate_limited` | Too many requests. Please try again later. | Rate limit per IP |

---

### 11. Forgot Password

Kirim kode reset password.

```
POST /core/v1/auth/forgot-password
```

**Auth:** Tidak diperlukan (rate limit per IP)

**Request:**
```json
{
  "email": "rina@timebox.id"
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `email` | string | Ya | Format email, max 254. Tidak case-sensitive |

**Response (200)** — selalu sama, apa pun alamatnya:
```json
{
  "data": {
    "expires_in": 900,
    "resend_available_in": 60
  },
  "message": "If an account uses this email, a password reset code has been sent to it."
}
```

> Kode dikirim ke akun **aktif** mana pun dengan email itu — terverifikasi atau belum, termasuk akun Google-only (begitulah user Google membuat password). Email tidak dikenal, akun nonaktif, atau kuota per jam habis → jawaban sama, tidak ada email. Kode baru membatalkan kode reset sebelumnya. Langkah berikutnya: [`POST /reset-password/verify`](#12-verify-reset-code).

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | `email` tidak ada / format salah |
| 429 | `resend_cooldown` | A code was sent moments ago — detail `Wait before asking for another` | Kode reset untuk alamat ini diminta kurang dari `OTP_RESEND_COOLDOWN` lalu; `retry_after` |
| 429 | `rate_limited` | Too many requests. Please try again later. | Rate limit per IP |

---

### 12. Verify Reset Code

Tukar kode reset dengan `reset_token`. Dua langkah ini membuat aplikasi baru meminta password baru setelah kodenya terbukti benar.

```
POST /core/v1/auth/reset-password/verify
```

**Auth:** Tidak diperlukan (rate limit per IP)

**Request:**
```json
{
  "email": "rina@timebox.id",
  "code": "583104"
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `email` | string | Ya | Format email, max 254. Tidak case-sensitive |
| `code` | string | Ya | Tepat 6 digit angka |

**Response (200):**
```json
{
  "data": {
    "reset_token": "9b2e4d7a0c1f...4a6c",
    "expires_in": 600
  },
  "message": "Code verified. Choose a new password."
}
```

| Field | Type | Keterangan |
|-------|------|------------|
| `reset_token` | string | 64 karakter hex, sekali pakai, terikat ke alamat akun. Backend hanya menyimpan hash SHA-256-nya. Jangan simpan di storage persisten |
| `expires_in` | int | Umur `reset_token` dalam detik — `RESET_TOKEN_EXPIRATION` (default 600) |

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | `email` / `code` tidak ada atau formatnya salah |
| 400 | `invalid_code` | Invalid code | Kode salah, atau kode lama yang sudah diganti kode baru (dihitung sebagai tebakan salah); `data: {"attempts_left": 4}` |
| 400 | `code_expired` | This code has expired — detail `Ask for a new code` | Email tidak dikenal atau milik akun nonaktif, kode terbaru kedaluwarsa / sudah dipakai, kode dibatalkan, atau alamat akun berubah |
| 429 | `too_many_attempts` | Too many wrong codes — detail `Ask for a new code` | Kode mati; `retry_after` = detik sampai [`/forgot-password`](#11-forgot-password) boleh dipakai lagi |
| 429 | `rate_limited` | Too many requests. Please try again later. | Rate limit per IP |

---

### 13. Reset Password

Set password baru dengan `reset_token`, lalu sign in di client ini.

```
POST /core/v1/auth/reset-password
```

**Auth:** Tidak diperlukan (rate limit per IP). Web: kirim `X-Timebox-Client: web` ([mode cookie](#klien-web-vs-desktop)).

**Request:**
```json
{
  "reset_token": "9b2e4d7a0c1f...4a6c",
  "new_password": "Kalender-Baru-8"
}
```

| Field | Type | Required | Keterangan |
|-------|------|----------|------------|
| `reset_token` | string | Ya | Dari [`/reset-password/verify`](#12-verify-reset-code), max 128 |
| `new_password` | string | Ya | Kebijakan sama dengan signup: 8–72 byte, huruf besar + huruf kecil + angka, bukan password umum |

**Response (200):** sesi baru untuk client ini, sama dengan [`/signin`](#1-sign-in) — message `Password has been reset`.

Efek, dalam satu transaksi:

- Password diganti (akun Google-only jadi punya password, `has_password: true`).
- Email ditandai terverifikasi — kode reset membuktikan user menguasai mailbox-nya.
- Lock sign in dan hitungan password salah di-reset.
- **Semua sesi sebelumnya diakhiri:** refresh token di-revoke, access token yang sudah terbit → `401 session_ended`. Sesi di response ini dibuat sesudahnya, jadi tetap berlaku.
- **Semua kode email lain yang belum dipakai batal** (verifikasi, ganti email).
- Email "password changed" dikirim.
- Kalau email akun **belum terverifikasi** sebelum reset, ini [klaim akun](#klaim-akun-email-belum-terverifikasi): **semua API key akun juga di-revoke**.

**Errors:**

| Status | `code` | Message | Kapan |
|--------|--------|---------|-------|
| 400 | `validation_failed` | Invalid request payload | `reset_token` tidak ada, atau `new_password` tidak memenuhi kebijakan (`errors.new_password`) |
| 400 | `invalid_reset_token` | This reset is invalid or has expired — detail `Ask for a new code` | `reset_token` tidak dikenal, sudah dipakai, kedaluwarsa, atau alamat akun berubah sejak kodenya diverifikasi. Mulai lagi dari [`/forgot-password`](#11-forgot-password) |
| 401 | `user_inactive` | User account is not active | Akun dinonaktifkan |
| 429 | `rate_limited` | Too many requests. Please try again later. | Rate limit per IP |

---

## Klaim Akun (Email Belum Terverifikasi)

Signup tidak membuktikan kepemilikan email: siapa pun bisa mendaftar dengan alamat orang lain. Karena itu akun dengan `is_email_verified=false` diperlakukan sebagai klaim yang belum terbukti, dan **siapa pun yang membuktikan menguasai alamat itu mengambil alih akunnya**:

| Jalur | Yang terjadi |
|---|---|
| [Google sign-in](#4-sign-in-with-google) dengan email yang diverifikasi Google | Akun Google di-link, password lama dihapus, email terverifikasi, lock dibuka, semua sesi, semua API key, dan semua kode email yang belum dipakai (termasuk ganti email) di-revoke, caller langsung sign in |
| [Reset password](#13-reset-password) dengan kode di email | Password lama diganti, email terverifikasi, lock dibuka, semua sesi, semua API key, dan semua kode email yang belum dipakai di-revoke, caller sign in dengan sesi baru |

- API key yang di-revoke mendapat `revoke_reason` `account claimed by the verified owner of its email`. Role akun tidak berubah.
- Akun yang emailnya sudah terverifikasi tidak diklaim: Google sign-in hanya me-link akun Google (tidak ada yang di-revoke), reset password hanya mengakhiri sesi (API key tetap).
- Admin tidak bisa meng-assign role ke user yang emailnya belum terverifikasi ([user.md](user.md#4-update-user)), jadi akses tidak bisa diberikan ke akun yang alamatnya belum terbukti.
- **[Verifikasi email](#9-verify-email) bukan klaim:** kode verifikasi menandai email terverifikasi dan membuat sesi, tapi tidak mencabut password, sesi, maupun API key yang sudah ada.
- **Sisa risiko:** kode verifikasi dikirim ke alamat yang didaftarkan. Kalau pemilik asli memasukkan kode yang tidak pernah ia minta (karena orang lain mendaftar dengan alamatnya), ia masuk ke akun si pendaftar, akun itu jadi terverifikasi dan tidak bisa diklaim lagi — password, sesi, dan API key pendaftar tetap hidup; Google sign-in pemilik asli sesudahnya hanya me-link, dan reset password tidak lagi me-revoke API key. Teks email verifikasi meminta penerima mengabaikannya kalau tidak merasa mendaftar.

---

## Authentication Flow

```
Email/Password:
  1. POST /auth/signup { email, password, username?, ... }
                                  → user + verification { email, expires_in, resend_available_in } (tanpa token)
  2. Email berisi kode 6 digit
  3. POST /auth/verify-email { email, code }
                                  → Sesi (token + user + roles + permissions)
     Kode hilang / kedaluwarsa    → POST /auth/verify-email/resend { email }
  Berikutnya: POST /auth/signin { login, password } → Sesi

Google (desktop / web, authorization code + PKCE):
  1. GET  /auth/google/client?platform=desktop|web → client_id (+ redirect_uris untuk web)
  2. App → Google (scope openid email profile, code_challenge S256)
     → redirect ke loopback http://127.0.0.1:<port>/… (desktop) atau redirect URI web
  3. POST /auth/google { code, code_verifier, redirect_uri, platform, timezone? } → Sesi
     - is_new_user=true  (201): akun baru dibuat (tanpa role)
     - is_new_user=false (200): user existing / akun dengan email sama di-link
       (yang emailnya belum terverifikasi sekaligus diklaim)

Lupa password:
  1. POST /auth/forgot-password { email }                 → Email berisi kode
  2. POST /auth/reset-password/verify { email, code }     → { reset_token, expires_in }
  3. POST /auth/reset-password { reset_token, new_password }
                                  → Sesi baru di client ini (semua sesi lama sudah diakhiri)

Ganti email (user sedang login, lihat account.md):
  1. POST /account/email { new_email, current_password | google }  → Kode ke alamat baru
  2. POST /account/email/confirm { code }                            → { user } dengan email baru
     Kode hilang                → POST /account/email/resend

Selanjutnya:
  - Semua request: Authorization: Bearer <access_token>
  - Web: X-Timebox-Client: web + credentials: 'include' di /auth/*; refresh token di cookie tb_refresh
  - GET  /auth/me                → (Opsional) Rehydrate profil + permissions setelah reload
  - POST /auth/refresh           → Saat 401 token_expired / sebelum expires_in habis; desktop simpan
                                   refresh token baru (sesi sama)
  - POST /auth/logout            → Logout device ini (access token sesi ini ikut mati)
    DELETE /account/sessions/:id → Logout satu device lain
    POST /auth/logout-all        → Logout semua device (access token ikut mati)
```

### Token Expiry

| Token | Default | Env |
|-------|---------|-----|
| Access token | 15 menit | `JWT_EXPIRATION` |
| Refresh token (dan cookie `tb_refresh`) | 7 hari (sejak terakhir di-refresh) | `JWT_REFRESH_EXPIRATION` |
| Kode email (verifikasi, reset, ganti email) | 15 menit, maks. 5 tebakan salah | `OTP_EXPIRATION`, `OTP_MAX_ATTEMPTS` |
| Jeda kirim ulang kode | 60 detik | `OTP_RESEND_COOLDOWN` |
| `reset_token` | 10 menit | `RESET_TOKEN_EXPIRATION` |

### Rekomendasi FE

- **Bercabang pada `code`, bukan `message`.** Hanya `401 token_expired` yang berarti "refresh lalu ulangi request". `401 session_ended` dan `401 user_inactive` (dari endpoint mana pun, termasuk `/refresh`) berarti sesi berakhir → buang token, arahkan ke halaman login. `401` lain — `invalid_credentials`, `google_auth_failed`, `google_email_not_verified`, `reauth_stale` — adalah input yang ditolak; sesi (kalau ada) tetap berlaku. Error [re-autentikasi](account.md#re-autentikasi) tidak pernah memakai `token_expired` atau `session_ended`.
- **Web:** kirim `X-Timebox-Client: web` dan `credentials: 'include'` di setiap request `/auth/*` dan `POST /account/delete`. Simpan access token di memory saja; setelah reload panggil `POST /auth/refresh` (cookie), lalu `GET /auth/me`. Jangan simpan `permissions` di localStorage.
- **Desktop:** jangan kirim `X-Timebox-Client`; simpan refresh token di OS keychain dan kirim di body `/refresh` dan `/logout`.
- Jadwalkan refresh sebelum `expires_in` habis, atau refresh saat mendapat `401 token_expired`. Jalankan refresh di satu tempat (lock lintas tab / window); refresh token lama langsung tidak berlaku.
- **Form kode:** input 6 digit angka (string, pertahankan `0` di depan). `invalid_code` → tampilkan `data.attempts_left`. `too_many_attempts` / `resend_cooldown` → nonaktifkan tombol kirim ulang selama `retry_after` detik. Mulai countdown tombol kirim ulang dari `resend_available_in`.
- `429`: tampilkan pesan dan jangan retry sebelum `retry_after` detik (sama dengan header `Retry-After`).
- `403 email_not_verified` saat sign in → arahkan ke form kode untuk `data.email`, dengan tombol [kirim ulang](#10-resend-verification-code).
- Kirim `timezone` = `Intl.DateTimeFormat().resolvedOptions().timeZone` saat signup dan Google sign-in.
- Tampilkan message `/forgot-password` dan `/verify-email/resend` apa adanya — jawabannya sengaja sama untuk email terdaftar maupun tidak.

---

## Google OAuth Setup

Google sign-in butuh OAuth client di Google Cloud Console — satu per platform — dan env di BE. Langkah lengkap untuk operator: [deployments/README.md](../../../deployments/README.md#google-oauth-client).

| Platform | Tipe OAuth client | Env BE | Redirect URI |
|---|---|---|---|
| `desktop` | **Desktop app** | `GOOGLE_DESKTOP_CLIENT_ID`, `GOOGLE_DESKTOP_CLIENT_SECRET` | Loopback `http://127.0.0.1:<port>/…` / `http://[::1]:<port>/…`, port bebas — tidak perlu didaftarkan |
| `web` | **Web application** | `GOOGLE_WEB_CLIENT_ID`, `GOOGLE_WEB_CLIENT_SECRET`, `GOOGLE_WEB_REDIRECT_URIS` | Didaftarkan di Google **dan** di `GOOGLE_WEB_REDIRECT_URIS`, persis sama (prod `https://app.timebox.id/oauth/google`, dev `http://localhost:5173/oauth/google`, `http://localhost:5174/oauth/google`) |

- Scope yang diminta aplikasi: `openid email profile`; `code_challenge_method=S256`.
- Client secret hanya di BE. Secret client "Desktop app" tidak rahasia menurut Google, tapi tetap dibutuhkan untuk penukaran code dan tetap disimpan di Secret.
- Platform yang belum dikonfigurasi menjawab `503 google_not_configured`; platform lain tetap jalan.

---

**Last Updated:** 2026-10-24 (kode OTP 6 digit menggantikan link di email: `/verify-email {email, code}` langsung sign in, reset password tiga langkah dengan `/reset-password/verify` dan `reset_token`, `/confirm-email-change` dihapus; Google sign-in langsung ke Google OAuth dengan PKCE (`GET /google/client`, `POST /google {code, code_verifier, redirect_uri, platform}`), Firebase dihapus; cookie `tb_refresh` untuk klien web; `code` di setiap error dan `retry_after` di setiap `429`; `email_not_verified` jadi `403`; user object baru)
**Previously:** 2026-10-23 (Google sign-in lewat identity ter-link ikut `EMAIL_VERIFICATION_REQUIRED`; reset / klaim / ganti password membatalkan link yang belum dipakai; request bersamaan tidak meninggalkan sesi hidup; refresh gagal tidak menghabiskan token; toleransi jam 5 detik), 2026-10-23 (claim `sid` dan sesi per device, `/logout` ikut mematikan access token sesinya, `/refresh` mempertahankan sesi dan menolak email belum terverifikasi saat `EMAIL_VERIFICATION_REQUIRED=true`, field `timezone`, `POST /confirm-email-change`, re-autentikasi `/account/*` ikut lockout, audit log), 2026-10-23 (verifikasi email & reset password lewat link, klaim akun dengan email belum terverifikasi lewat Google / reset password menggantikan `409`, email notifikasi lock / password diganti / welcome, lockout ikut menghitung ganti password), 2026-10-23 (Timebox baseline: permission `users:*`, access token 15 menit, satu pesan untuk semua sign-in gagal, kebijakan password, logout-all/ganti password mematikan access token, `/logout` & `/me` menolak API key), 2026-10-22 (rotasi refresh token + deteksi reuse, lockout configurable, rate limit per IP, status user dicek per request, signup tidak menandai email terverifikasi), 2026-10-21 (company / client / branch dihapus, user baru tanpa role), 2026-05-16 (Sign in with Google), 2026-04-20 (permissions keluar dari JWT ke cache Redis)
