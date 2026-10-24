# Update 2026-10-24 — Kode OTP, Google OAuth tanpa Firebase, cookie web, avatar

Perombakan auth setelah rilis hardening 2026-10-23 (commit `724444a`): link di email diganti kode 6 digit, Google sign-in langsung ke Google OAuth (Firebase dihapus), refresh token aplikasi web pindah ke cookie httpOnly, setiap error membawa `code`, dan foto profil di-upload ke R2. Perubahan ini **belum di-commit** saat catatan ini ditulis.

Detail kontrak API ada di [docs/api-contract](../api-contract/README.md) (bagian "⚠️ Breaking / Behaviour Change 2026-10-24" di [auth.md](../api-contract/core/auth.md), [account.md](../api-contract/core/account.md), [user.md](../api-contract/core/user.md), [audit-log.md](../api-contract/core/audit-log.md), dan daftar kode error di [README](../api-contract/README.md#error-codes)); panduan deploy di [deployments/README.md](../../deployments/README.md); semua env di [`.env.example`](../../.env.example).

---

## Ringkasan

| Area | Sebelumnya | Sekarang | Alasan |
|---|---|---|---|
| Verifikasi email | Link `FRONTEND_URL/verify-email?token=` (48 jam); setelah verifikasi user sign in manual | Kode 6 digit (15 menit, maks. 5 tebakan); `POST /auth/verify-email {email, code}` langsung membuat sesi | Aplikasi desktop tidak bisa menerima link dengan aman; kode diketik di aplikasi yang memintanya |
| Reset password | Link `?token=` (1 jam) → set password → sign in manual | `forgot-password` → kode → `reset-password/verify` → `reset_token` (10 menit) → `reset-password` → sesi baru, sesi lain diakhiri | Password baru diminta setelah kode terbukti benar; user langsung masuk |
| Ganti email | Link ke alamat baru, dikonfirmasi lewat endpoint publik `POST /auth/confirm-email-change` | Kode ke alamat baru, dikonfirmasi user yang sedang login di `POST /account/email/confirm`; kirim ulang tanpa re-autentikasi di `POST /account/email/resend` | Konfirmasi terikat ke sesi yang meminta, bukan ke siapa pun yang memegang link |
| Penyimpanan kode | SHA-256 token link 32 byte acak | HMAC-SHA256 berkunci (turunan `JWT_SECRET`) atas id baris + kode; kolom `attempts`; jeda kirim ulang per alamat | Enam digit terlalu sedikit untuk hash biasa — tanpa kunci bisa ditebak offline dari dump database |
| Google sign-in | Firebase ID token (`FIREBASE_PROJECT_ID`, service account) | OAuth langsung: authorization code + PKCE, client "Desktop app" (redirect loopback) dan "Web application"; BE menukar code dengan client secret | Popup Firebase tidak cocok untuk aplikasi Electron; satu layanan dan satu kredensial service account berkurang |
| Identity Google | Dikunci uid Firebase | Dikunci id akun Google (`sub`); migration `000012` memindahkan identity lama | `sub` stabil walau email akun Google berganti, dan tidak tergantung project Firebase |
| Re-autentikasi Google | `google_id_token` (Firebase, `auth_time` < 5 menit) | Objek `google {code, code_verifier, redirect_uri, platform}` | Mengikuti alur OAuth baru |
| Link / unlink Google | Hanya otomatis saat Google sign-in dengan email yang sama | `POST /account/google/link` (password wajib), `DELETE /account/google`; email notifikasi ke pemilik akun | User bisa mengelola akun Google-nya sendiri; sesi saja tidak bisa menambah jalan masuk |
| Foto profil | `avatar_url` bebas lewat `PUT /users/me`; akun Google menyimpan URL `lh3.googleusercontent.com` | Upload `PUT` / `DELETE /users/me/avatar` → JPEG ≤512 px di R2; foto Google disalin ke R2 | CSP aplikasi desktop hanya mengizinkan origin R2; URL bebas bisa dipakai melacak siapa yang membuka profil |
| Refresh token web | Di body, disimpan JavaScript | Header `X-Timebox-Client: web` → cookie `tb_refresh` (HttpOnly, `SameSite=Strict`, `Path=/core/v1/auth`), tidak ada di body; desktop tetap body | XSS tidak bisa membaca refresh token |
| Logout | `refresh_token` wajib di body | Opsional: body → cookie → sesi access token itu sendiri | Aplikasi web tidak memegang refresh token |
| Error | `message` + `errors.detail`; FE bercabang pada teks | `code` stabil di setiap error, `retry_after` di setiap `429` (kecuali `account_locked`), `data` untuk `attempts_left` / `email` | FE tidak bergantung pada teks pesan; hanya `token_expired` yang berarti refresh |
| Email belum terverifikasi | `401 Email not verified` | `403 email_not_verified` + `data.email` | `401` dicadangkan untuk kredensial / sesi; FE tahu alamat tujuan kode walau sign in dengan username |
| Signup | `username` wajib; response `{message, user}` | `username` opsional (diturunkan dari email); response `{user, verification}` | Form signup lebih pendek; FE langsung tahu umur kode dan countdown kirim ulang |
| SMTP | Aktif kalau host, user, password, dan from terisi | Aktif dengan `SMTP_HOST` + `SMTP_FROM_EMAIL`; user/password opsional (berdua); plain text hanya ke loopback tanpa kredensial | Mailpit lokal tanpa kredensial, tanpa membuka jalan kirim kredensial tanpa TLS |

---

## Detail perubahan

### 1. Kode OTP di email

- **Kode 6 digit** (bisa diawali `0`) untuk verifikasi email, reset password, dan ganti email. Juga ada di subject email.
- **Umur** `OTP_EXPIRATION` (default `15m`), sekali pakai; kode baru me-retire kode lama tujuan yang sama. Kode lama yang dimasukkan dibandingkan dengan kode terbaru → `invalid_code` (dihitung satu tebakan salah), bukan `code_expired`.
- **Tebakan salah** → `400 invalid_code` dengan `data.attempts_left`. Tebakan ke-`OTP_MAX_ATTEMPTS` (default 5) mematikan kode → `429 too_many_attempts` dengan `retry_after` (detik sampai kode baru boleh diminta). Tebakan dicek satu per satu (baris kode dikunci), jadi burst paralel tidak mendapat tebakan ekstra.
- **Jeda kirim ulang** `OTP_RESEND_COOLDOWN` (default `1m`) per alamat per tujuan — juga untuk alamat yang tidak terdaftar, sehingga jawaban `/verify-email/resend` dan `/forgot-password` tidak membocorkan apakah akun ada → `429 resend_cooldown`. Ganti email: jeda per akun. Response yang mengirim kode membawa `expires_in` dan `resend_available_in`.
- **Kuota per jam** tetap: `MAX_VERIFICATION_EMAILS` (5) dan `MAX_PASSWORD_RESET_EMAILS` (3) — lewat batas dijawab sama tanpa mengirim; kode ganti email memakai batas `MAX_VERIFICATION_EMAILS` dan dijawab `429 email_quota_exceeded`.
- **`code_expired`** = tidak ada kode yang bisa dicek: kode terbaru kedaluwarsa / sudah dipakai, kode dibatalkan (ganti / reset password, klaim), tidak pernah dikirim, atau alamatnya tidak dikenal. **Alamat akun nonaktif** di `/auth/verify-email` dan `/auth/reset-password/verify` juga dijawab `code_expired` (bukan `user_inactive`), sama dengan alamat tidak dikenal — jawabannya tidak membocorkan akun mana yang ada. `user_inactive` tetap muncul dari sign in, refresh, Google, `reset-password` (langkah `reset_token`), dan middleware.
- **Verifikasi email = sign in.** `POST /auth/verify-email` membuat sesi (berlaku juga dengan `EMAIL_VERIFICATION_REQUIRED=true`).
- **Reset password tiga langkah**, `reset_token` 64 hex sekali pakai (`RESET_TOKEN_EXPIRATION`, default `10m`), terikat ke alamat akun. Reset mengakhiri semua sesi lama, me-retire semua kode, memverifikasi email, membuka lock, dan (untuk akun yang emailnya belum terverifikasi) tetap menjadi **klaim akun** — API key di-revoke. Response-nya sesi baru untuk client yang mereset.
- **Kode ganti email** dikirim ke alamat baru, terikat ke alamat lama saat diminta; `POST /account/email/resend` selama kode terakhir belum dipakai dan belum kedaluwarsa — juga setelah mati karena tebakan salah (`409 email_change_not_pending` kalau tidak; penolakan itu tidak memulai jeda kirim ulang). Jeda kirim ulang dicek **sebelum** re-autentikasi, supaya code Google tidak terbuang oleh `429`.
- Dihapus: `POST /auth/confirm-email-change`, halaman FE `/verify-email`, `/reset-password`, `/confirm-email-change` yang membaca `?token=`.

### 2. Google OAuth (tanpa Firebase)

- `GET /auth/google/client?platform=desktop|web` → `{platform, client_id, redirect_uris}` (`redirect_uris` = `GOOGLE_WEB_REDIRECT_URIS` untuk web, `null` untuk desktop).
- `POST /auth/google {code, code_verifier, redirect_uri, platform, timezone?}` → sesi; `201` + `is_new_user: true` untuk akun baru. BE menukar code di `https://oauth2.googleapis.com/token` dengan client secret platform itu (batas 10 detik), lalu memverifikasi ID token (signature, audience = client platform, issuer, expiry).
- **Aturan redirect:** desktop harus `http://127.0.0.1:<port>/…` atau `http://[::1]:<port>/…` (port wajib, `localhost` ditolak — rekomendasi Google); web harus persis salah satu `GOOGLE_WEB_REDIRECT_URIS`. Selain itu `400 invalid_redirect_uri` sebelum apa pun dikirim ke Google.
- Scope yang diminta aplikasi: `openid email profile`, `code_challenge_method=S256`.
- **Satu akun Google per akun Timebox**, dan sebaliknya (dijaga aplikasi). Akun dengan email sama yang sudah me-link akun Google lain → `409 google_account_conflict`.
- **Link dari pengaturan:** `POST /account/google/link` butuh `current_password` (selalu — sesi saja tidak boleh menambah jalan masuk) → `409 google_already_linked` / `google_account_in_use`. **Unlink:** `DELETE /account/google`, ditolak `409 no_password` untuk akun tanpa password. Keduanya mengirim email ke alamat akun; audit `auth.google_linked` / `auth.google_unlinked`.
- **Foto profil Google disalin ke R2** (≤5 detik, hanya dari `*.googleusercontent.com` lewat https, ≤5 MiB) untuk akun baru dan saat link ke akun tanpa foto. Gagal → sign in tetap jalan tanpa foto.
- Error baru: `google_not_configured` (503), `google_auth_failed` (401), `invalid_redirect_uri` (400), `google_email_not_verified` (401), `google_account_conflict` (409), `google_account_not_linked` (403), `google_already_linked` (409), `google_account_in_use` (409), `google_not_linked` (404), `reauth_stale` (401).
- `pkg/firebase` dan dependency Firebase Admin dihapus.

### 3. Cookie refresh untuk aplikasi web

- Request dengan `X-Timebox-Client: web`: `/signin`, `/google`, `/verify-email`, `/reset-password`, dan `/refresh` memasang cookie `tb_refresh` (`HttpOnly`, `Path=/core/v1/auth`, `SameSite=Strict`, `Secure` kecuali `ENV=development`, `Max-Age` = `JWT_REFRESH_EXPIRATION`) dan mengosongkan `refresh_token` di body.
- `/refresh` tanpa token di body membaca cookie, **hanya** kalau request membawa header web **dan** `Origin` yang ada di `CORS_ALLOWED_ORIGINS` → selain itu `403 origin_not_allowed`. Origin di luar daftar sudah ditolak CORS (`403` tanpa body).
- `/logout`, `/logout-all`, `POST /account/delete`, dan `/refresh` yang gagal karena sesi berakhir menghapus cookie (kalau request membawa header web).
- CORS mengizinkan header `X-Timebox-Client` dan mengekspos `Retry-After`.
- Aplikasi desktop (Electron main process, tanpa `Origin`) tetap mengirim refresh token di body.
- **Syarat production:** API dan aplikasi web satu *site* (mis. `api.timebox.id` dan `app.timebox.id`), kalau tidak `SameSite=Strict` membuat browser tidak mengirim cookie.

### 4. Kode error

- Envelope error: `{"data": null | {...}, "message", "code", "errors"?, "retry_after"?}`. `code` selalu ada di error dan tidak pernah di response sukses.
- Kode generik per status: `validation_failed`, `bad_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`, `payload_too_large`, `rate_limited`, `internal_error`, `service_unavailable`. Kode kredensial dari middleware: `token_expired`, `session_ended`, `user_inactive`. Kode khusus auth / account / user: lihat [README](../api-contract/README.md#error-codes).
- **Hanya `token_expired` = refresh lalu ulangi.** `session_ended` / `user_inactive` = sign in lagi. Refresh token yang tidak dikenal, kedaluwarsa, atau di-revoke sekarang `401 session_ended` (sebelumnya pesan berbeda-beda tanpa kode). Error re-autentikasi tidak pernah memakai `token_expired` / `session_ended`.
- Setiap `429` membawa `retry_after` (detik, ≥ 1) selain header `Retry-After` — kecuali `account_locked` (lihat [Belum dikerjakan](#belum-dikerjakan)).

### 5. Avatar & R2

- `PUT /users/me/avatar` (multipart `file`: JPEG / PNG / WebP, ≤4096 px, body ≤2 MiB) → di-decode, diperkecil ke ≤512 px, di-encode ulang jadi JPEG (metadata EXIF/GPS terbuang), disimpan di `avatars/<user_id>/<uuid>.jpg`. `DELETE /users/me/avatar` mengosongkan dan menghapus file. `avatar_url` di `PUT /users/me` / `PUT /users/:id` diabaikan.
- Butuh R2 (`R2_ACCOUNT_ID`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_BUCKET`) **dan** `R2_PUBLIC_URL`; tanpa itu `503 storage_not_configured` dan foto Google tidak disalin. `avatar_url` selalu berorigin `R2_PUBLIC_URL` — CSP `img-src` aplikasi desktop harus mengizinkannya.
- Hapus akun sendiri ikut menghapus file foto di R2.

### 6. Email

- Template baru: `verification_code`, `reset_password_code`, `email_change_code`, `google_linked`, `google_unlinked`; template link lama dihapus.
- Dua pengirim: email kode dari `SMTP_FROM_EMAIL` (`no-reply@`), email notifikasi (welcome, akun terkunci, password / email diganti, akun ditutup, Google tertaut / dilepas) dari `SMTP_NOTIFICATIONS_FROM_EMAIL` (`notifications@`; kosong = ikut `SMTP_FROM_EMAIL`).
- SMTP aktif dengan `SMTP_HOST` + `SMTP_FROM_EMAIL`; `SMTP_USER` / `SMTP_PASSWORD` opsional tapi berdua. Koneksi wajib TLS (465) / STARTTLS, kecuali ke host loopback tanpa kredensial (Mailpit: `SMTP_HOST=localhost`, `SMTP_PORT=1025`, UI `http://localhost:8025`).
- Tanpa SMTP, kode ditulis di log level debug (hanya development).

### 7. Lain-lain

- Objek `user` di response auth / account selalu lengkap: `full_name` dan `avatar_url` dikirim `null` (sebelumnya tidak dikirim), field baru `is_email_verified` dan `google`.
- Sesi dari aplikasi desktop (UA `timebox/…` / `Electron/…`) tampil sebagai `Timebox Desktop on <OS>` di `GET /account/sessions`.
- Audit log: `auth.google_linked`, `auth.google_unlinked`, `user.avatar_changed`, `user.avatar_removed`; `auth.sign_in` dengan `method` `email_code` dan `password_reset`.
- Rate limit per IP mendapat dua bucket baru: `auth:google-client`, `auth:reset-password-verify`.

---

## Migration `000012_email_codes_google_oauth`

**Up:**

1. `core.account_tokens`:
   - kolom baru `attempts SMALLINT NOT NULL DEFAULT 0` (tebakan salah),
   - `CHECK` purpose ditambah `reset_grant` (token hasil kode reset yang benar),
   - komentar tabel/kolom diperbarui (kode = HMAC-SHA256 berkunci, `reset_grant` = SHA-256).
2. `core.user_identities` Google: `provider_user_id` diganti dari uid Firebase ke id akun Google (`sub`) yang tersimpan di claim Firebase `raw_profile.firebase.identities["google.com"][0]` — hanya untuk baris yang punya nilai itu dan berbeda. User lama tetap masuk ke akun yang sama.

**Expand/contract:** rilis sebelumnya tetap jalan di atas skema ini (`attempts` punya default; baris `reset_grant` tidak dibacanya). Tapi Google sign-in rilis lama (Firebase) tidak lagi menemukan identity yang sudah dipindah — selama rolling update, Google sign-in yang jatuh ke pod lama me-link ulang lewat email (baris identity kedua berisi uid Firebase untuk user yang sama). Tidak berbahaya, tapi FE lama dan BE baru juga tidak kompatibel (`id_token` → `400`), jadi rilis FE dan BE bersamaan dan sebaiknya di jam sepi.

**Cek setelah up** (baris yang tidak bisa dipindah tetap berisi uid Firebase; pemiliknya akan mendapat `409 google_account_conflict` saat Google sign-in, karena akunnya terlihat sudah me-link akun Google lain):

```sql
-- Identity Google yang masih uid Firebase (tidak punya claim google.com)
SELECT id, user_id, email, created_at
FROM core.user_identities
WHERE provider = 'google'
  AND raw_profile ? 'firebase'
  AND raw_profile #>> '{firebase,identities,google.com,0}' IS NULL;

-- User dengan lebih dari satu identity Google (mis. dari pod lama selama rollout)
SELECT user_id, count(*) FROM core.user_identities
WHERE provider = 'google' GROUP BY user_id HAVING count(*) > 1;
```

Baris dari query pertama boleh dihapus: pemiliknya me-link ulang dengan Google sign-in (email yang sama, diverifikasi Google) atau lewat `POST /account/google/link`. Untuk query kedua, hapus baris yang `provider_user_id`-nya uid Firebase (sama dengan `raw_profile->>'user_id'`).

**Down (rollback):**

1. Identity yang masih membawa claim Firebase (`raw_profile ? 'firebase'`) dikembalikan ke uid Firebase (`raw_profile->>'user_id'`). Identity yang sudah sign in lewat Google OAuth sesudah up kehilangan claim Firebase (snapshot ditimpa claim Google) dan tetap memakai `sub` — rilis lama menemukannya lewat email (me-link ulang), atau membuat akun baru kalau email akun Google berbeda dengan email akun.
2. **Semua kode dan `reset_grant` yang belum dipakai dihapus** — kode tidak bisa dipakai sebagai link. User yang sedang di tengah verifikasi / reset / ganti email harus meminta ulang (link).
3. `CHECK` purpose dikembalikan, kolom `attempts` di-drop, komentar dikembalikan.

Rollback juga butuh env lama lagi (`FIREBASE_PROJECT_ID`, `FIREBASE_CREDENTIALS_JSON`; URL link default dari `FRONTEND_URL`). Foto di R2 tetap ada dan URL-nya tetap valid.

---

## Env yang perlu diisi operator

| Variable | Wajib? | Catatan |
|---|---|---|
| `GOOGLE_DESKTOP_CLIENT_ID`, `GOOGLE_DESKTOP_CLIENT_SECRET` | Untuk Google sign-in di aplikasi desktop | OAuth client tipe **Desktop app**. Secret di Secret |
| `GOOGLE_WEB_CLIENT_ID`, `GOOGLE_WEB_CLIENT_SECRET`, `GOOGLE_WEB_REDIRECT_URIS` | Untuk Google sign-in di aplikasi web | Client tipe **Web application**; redirect production `https://app.timebox.id/oauth/google`, development `http://localhost:5173/oauth/google`, `http://localhost:5174/oauth/google` — persis sama di Google Cloud Console dan env. Langkah: [Google OAuth client](../../deployments/README.md#google-oauth-client) |
| `SMTP_HOST`, `SMTP_FROM_EMAIL` (+ `SMTP_USER`, `SMTP_PASSWORD`) | Ya di production | Tanpa SMTP tidak ada kode yang terkirim — user tidak bisa verifikasi maupun reset password. User / password diisi nilai asli, bukan base64 |
| `SMTP_NOTIFICATIONS_FROM_EMAIL` | Tidak | Pengirim email notifikasi, mis. `notifications@timebox.id`; kosong = `SMTP_FROM_EMAIL` |
| `R2_ACCOUNT_ID`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_BUCKET`, `R2_PUBLIC_URL` | Untuk foto profil | `R2_PUBLIC_URL` = custom domain bucket, mis. `https://cdn.timebox.id`. Langkah: [R2 custom domain untuk avatar](../../deployments/README.md#r2-custom-domain-untuk-avatar) |
| `CORS_ALLOWED_ORIGINS` | Ya untuk aplikasi web | Origin web yang boleh refresh lewat cookie |
| `OTP_EXPIRATION`, `OTP_MAX_ATTEMPTS`, `OTP_RESEND_COOLDOWN`, `RESET_TOKEN_EXPIRATION` | Tidak | Default `15m`, `5`, `1m`, `10m` |

**Dihapus** (boleh dibuang dari ConfigMap / Secret): `FIREBASE_PROJECT_ID`, `FIREBASE_CREDENTIALS_JSON`, `EMAIL_VERIFICATION_URL`, `RESET_PASSWORD_URL`, `EMAIL_CHANGE_URL`, `EMAIL_VERIFICATION_EXPIRATION`, `PASSWORD_RESET_EXPIRATION`.

---

## Breaking change untuk client

### Aplikasi web dan desktop

- **Bercabang pada `code`**, bukan `message`. `401 token_expired` → refresh lalu ulangi; `401 session_ended` / `user_inactive` → halaman sign in; `401` lain (mis. `invalid_credentials`, `google_auth_failed`) bukan akhir sesi.
- `429`: tunggu `retry_after` (body) / `Retry-After` (header).
- **Signup:** `username` boleh dikosongkan; response `{user, verification}` tanpa token → tampilkan form kode untuk `verification.email`, countdown kirim ulang dari `resend_available_in`.
- **Verifikasi email:** form 6 digit → `POST /auth/verify-email {email, code}` → simpan sesi (sama dengan sign in). Tangani `invalid_code` (`data.attempts_left`), `code_expired`, `too_many_attempts`, `resend_cooldown`.
- **Sign in:** `403 email_not_verified` (bukan `401`) → form kode untuk `data.email`.
- **Lupa password:** `forgot-password` → form kode → `reset-password/verify` → form password baru dengan `reset_token` → `reset-password` → sesi.
- **Ganti email:** `POST /account/email` → form kode → `POST /account/email/confirm {code}` → simpan `data.user`. Hapus halaman `/confirm-email-change`.
- **Google:** hapus Firebase SDK. `GET /auth/google/client?platform=…` → PKCE ke Google → `POST /auth/google {code, code_verifier, redirect_uri, platform, timezone}`. Re-autentikasi: kirim objek `google {…}`, bukan `google_id_token`. Pengaturan akun: tombol link / unlink Google (`POST /account/google/link` dengan password, `DELETE /account/google`).
- **Objek `user`:** `full_name` / `avatar_url` bisa `null`; baca `is_email_verified` dan `google`.
- **Foto profil:** upload lewat `PUT /users/me/avatar` (multipart `file`); `avatar_url` di `PUT /users/me` diabaikan.

### Khusus aplikasi web

- Kirim `X-Timebox-Client: web` dan `credentials: 'include'` di request `/auth/*` dan `POST /account/delete`. Jangan simpan refresh token (tidak ada di body); setelah reload: `POST /auth/refresh` (body kosong) lalu `GET /auth/me`.
- Halaman `/oauth/google` di redirect URI yang terdaftar.

### Khusus aplikasi desktop

- Jangan kirim `X-Timebox-Client`; refresh token tetap di body, simpan di OS keychain.
- Google: server HTTP loopback sementara di `127.0.0.1:<port acak>`, buka URL otorisasi di browser sistem.
- CSP `img-src` harus mengizinkan origin `R2_PUBLIC_URL` (mis. `https://cdn.timebox.id`).

---

## Verifikasi yang perlu dijalankan sebelum commit

- `make lint` dan `go test -race ./...`.
- `make test-integration` dengan `TEST_DATABASE_URL` (database yang sudah dimigrasi sampai `000012`).
- Migration `000012`: naik → turun → naik di salinan data production, lalu jalankan query cek di atas.
- Smoke test: signup → kode di Mailpit → verify-email; forgot → reset-password/verify → reset-password; Google sign-in desktop (loopback) dan web (redirect terdaftar); refresh web lewat cookie dari origin yang diizinkan dan `403 origin_not_allowed` dari yang tidak; upload avatar dengan dan tanpa R2.

---

## Belum dikerjakan

- **`429 account_locked` tanpa `retry_after` / `Retry-After`**, berbeda dari `429` lain — FE tidak tahu kapan lock berakhir (hanya dari email ke pemilik).
- **Verifikasi email bukan klaim akun:** pemilik asli yang memasukkan kode verifikasi di akun yang didaftarkan orang lain dengan alamatnya ikut masuk ke akun itu, tanpa mencabut password / sesi / API key si pendaftar (lihat [Klaim akun](../api-contract/core/auth.md#klaim-akun-email-belum-terverifikasi)).
- **Unlink Google tanpa re-autentikasi** — sesi saja cukup (pemilik dikirimi email); link Google butuh password.
- **Identity Google yang tidak bisa dipindah** oleh migration `000012` membuat pemiliknya mendapat `409 google_account_conflict` sampai barisnya dihapus (lihat query cek di atas).
- Warisan dari 2026-10-23 yang masih terbuka: rate limit per user untuk `/account/*`, re-autentikasi untuk membuat API key, job retensi data user yang dihapus admin, outbox email di PostgreSQL, Jenkinsfile dan manifest Kubernetes.

---

## File utama yang berubah

| Area | File |
|---|---|
| Auth | `internal/modules/core/auth/{main.auth.go,handler/{auth.handler,cookie,errors}.go,dto/*.go,service/{auth.service,account,codes,email_change,google,reauth,close_account}.go,repository/account_token.repository.go,domain/account_token.go}` |
| User | `internal/modules/core/user/{main.user,handler/user.handler,service/user.service,repository/{user,user_identity}.repository,dto/user.dto,domain/user_identity}.go` |
| Google / avatar | `pkg/googleauth/` (baru), `pkg/avatar/` (baru), `pkg/firebase/` (dihapus) |
| Email | `pkg/email/{email,smtp,transport}.go`, `pkg/email/templates/*_code.html`, `google_{linked,unlinked}.html` (baru; template link dihapus), `internal/shared/mailer/async.go` |
| Shared | `internal/shared/{response/response,request/request,audit/audit,ratelimit/limiter}.go`, `internal/middleware/{auth,rate_limit}.go`, `pkg/token/generator.go` |
| Config & wiring | `internal/config/config.go`, `internal/router/router.go`, `go.mod`, `go.sum`, `Dockerfile` |
| Database | `internal/database/migrations/core/000012_email_codes_google_oauth.{up,down}.sql` |
| Test | `internal/modules/core/auth/handler/auth.handler_test.go`, `internal/modules/core/auth/service/{google_integration_test,…}.go`, `internal/modules/core/user/handler/user.handler_test.go`, `pkg/googleauth/googleauth_test.go`, `pkg/avatar/*_test.go`, `pkg/token/generator_test.go`, plus test email / mailer / ratelimit |
| Dokumen | `docs/api-contract/**`, `docs/erd/*`, `docs/updates/2026-10-24-otp-google-oauth-avatar.md`, `README.md`, `deployments/README.md`, `.env.example` |
