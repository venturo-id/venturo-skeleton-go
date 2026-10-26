# Arsitektur

Ringkasan yang dipindah dari `CLAUDE.md` lama. Isinya bisa dibaca dari kode, jadi
tidak dimuat otomatis ke sesi agent; buka file ini bila butuh gambaran besar.
Perilaku endpoint yang mengikat ada di [docs/api-contract](api-contract/README.md),
skema database di [docs/erd](erd/README.md).

## Gambaran umum

Timebox Backend adalah fondasi auth + RBAC produk Timebox: API single-tenant dengan
Go, Gin, PostgreSQL (pgx) dan Redis, berbentuk modular monolith. Tidak ada lapisan
tenant (client/company/branch) — akses hanya diatur role global.

- **Redis wajib** (authz cache dan rate limit): API tidak start tanpanya.
- **MongoDB opsional**, hanya untuk audit log (`MONGO_URI`; tanpa itu tidak ada yang dicatat).

## Struktur modul

Setiap modul fitur ada di `internal/modules/{domain}/{module}/`:

```
module_name/
├── domain/              # entity bisnis
├── dto/                 # request/response DTO + tag validasi
├── handler/             # handler HTTP (Gin)
├── repository/          # akses data (pgx)
├── service/             # logika bisnis
└── main.{module}.go     # Initialize() dan SetupRoutes()
```

`Initialize(...) *Module` menerima semua dependency sebagai argumen (constructor
injection). Repository yang dipakai lintas modul (users, roles) dibangun sekali di
[internal/router/router.go](../internal/router/router.go). Backend middleware (authz
service, validator API key, rate limiter) didaftarkan sekali di `router.go` lewat
`middleware.Set*`.

## Route

Semua modul diinisialisasi di [internal/router/router.go](../internal/router/router.go):

- `GET /health` (liveness, statis) dan `GET /ready` (ping Postgres + Redis; 503 bila
  salah satu mati atau saat draining setelah SIGTERM — pool yang jenuh tetap dianggap
  ready supaya beban tidak menarik semua replika sekaligus).
- Core: `/core/v1/auth`, `/core/v1/account`, `/core/v1/users`, `/core/v1/roles`,
  `/core/v1/api-keys`, `/core/v1/audit-logs`.

Setiap request melewati panic recovery (tidak pernah me-log header), konteks audit
(IP, user agent), CORS dari `CORS_ALLOWED_ORIGINS`, dan batas body 2 MiB.

## Model akses

1. Rantai middleware: `JWTAuth()` → `RequirePermission()` (atau `RequireAnyPermission`,
   `RequireAllPermissions`). Endpoint khusus sesi menambah `DenyAPIKeyAuth()`;
   endpoint publik yang perlu throttle memakai `RateLimitByIP`.
2. Role global di `core.user_roles`; permission efektif = gabungan permission semua
   role aktif user.
3. Kode permission `<resource>:<action>`. Resource terdaftar di
   [internal/shared/authz/resources.go](../internal/shared/authz/resources.go). Level per
   resource: `viewer` → read; `editor` → create, read, update, delete; `admin` →
   editor + export, import, restore.
4. Subject authz live: tiap request `JWTAuth` memuat `authz.Subject` (aktif, super
   admin, permission, session epoch) dari Redis. User nonaktif/terhapus mendapat 401
   walau token belum kedaluwarsa. Counter versi mencegah request yang membaca DB
   tepat sebelum invalidasi menulis balik subject basi.
5. Session epoch `users.tokens_valid_after`: mengakhiri semua sesi me-revoke semua
   refresh token dan membuat access token yang terbit sebelumnya 401. Satu sesi =
   rantai refresh token dengan `session_id` sama, dibawa access token sebagai claim
   `sid`.
6. Penerbitan/refresh sesi dan pengakhiran sesi berserialisasi pada baris user
   (`UserRepository.FindByIDForUpdate`) di transaksi yang menerbitkan atau me-revoke.
7. Grant ceiling: apa pun yang memberi akses atau mengelola user lain memeriksa
   `caller.Covers(perms)`.
8. API key (`X-API-Key: tbx_<env>_<key_id>_<secret>`) bertindak dengan scope ∩
   permission pemilik saat ini — tidak pernah sebagai super admin — dan di-rate limit
   per key.
9. User baru tanpa role. Role hanya bisa di-assign ke email terverifikasi.
10. IP client dari `X-Forwarded-For` hanya untuk proxy di `TRUSTED_PROXIES`.
11. Re-autentikasi (`reauthenticate`, lalu `confirmReauthTx` di transaksi perubahan)
    untuk perubahan yang bisa menyerahkan akun atau datanya ke orang lain: password
    saat ini, atau authorization code Google sign-in baru (PKCE) dari akun Google
    yang ter-link.
12. Email belum terverifikasi hanya klaim: yang membuktikan kontrol alamat (Google
    sign-in dengan email terverifikasi Google, atau kode reset) mengambil alih akun
    dan mengakhiri semua akses sebelumnya (`endPriorAccessTx` di
    [account.go](../internal/modules/core/auth/service/account.go)).

## Alur autentikasi

Detail lengkap (request, response, kode error) ada di
[auth.md](api-contract/core/auth.md) dan [account.md](api-contract/core/account.md).
Ringkasnya: signup tanpa role + kode verifikasi 6 digit; sign in password atau Google
OAuth (authorization code + PKCE); refresh merotasi token dalam sesi yang sama;
logout satu sesi atau semua; reset password dengan kode → `reset_token` → password
baru; self-service akun di `/core/v1/account`.

Access token HS256, issuer `timebox-api`, default 15 menit (`JWT_EXPIRATION`).
Permission dibaca dari Redis, tidak di-embed di token.

Lock password: sign-in dan ganti password berbagi satu lock (`MAX_LOGIN_ATTEMPTS`,
`ACCOUNT_LOCKOUT_DURATION`). Setiap pengecekan password dihitung sebelum
dibandingkan (`reservePasswordAttempt` → `UserRepository.ReservePasswordAttempt`,
satu `UPDATE` atomik), dan password benar mengakhiri hitungan
(`ClearPasswordAttempts`) yang terikat ke hash yang dibandingkan.

## Paket siap pakai

- [pkg/storage](../pkg/storage/): Cloudflare R2 lewat S3 API (`Upload`, `Delete`,
  `PresignPut`, `PresignGet`, `PublicURL`); dibangun dengan `storage.NewR2(cfg.Storage)`
  bila `cfg.Storage.Enabled()`.
- [pkg/email](../pkg/email/): `email.EmailService` dengan template ter-embed; SMTP via
  `email.NewSMTPEmailService(cfg.Email)` bila `cfg.Email.Enabled()`, selain itu
  `NoOpEmailService`.
- [internal/shared/mailer](../internal/shared/mailer/): `mailer.Async` membungkus
  `EmailService` agar setiap kirim langsung kembali (maks 8 paralel, 256 antre, sisanya
  dibuang dan di-log). Dibangun sekali oleh `router.NewMailer(cfg)` di `cmd/api`,
  di-drain `Wait(ctx)` saat shutdown.
- [internal/shared/audit](../internal/shared/audit/): audit log MongoDB (antre, ditulis
  batch di background, kedaluwarsa setelah `AUDIT_LOG_RETENTION`) atau `Discard` tanpa
  `MONGO_URI`. Diinjeksi sebagai `audit.Recorder` / `audit.Reader`.

## Batas runtime

- Setiap sesi database: `statement_timeout` 30 detik,
  `idle_in_transaction_session_timeout` 60 detik, nama aplikasi `timebox-api`
  ([internal/database/database.go](../internal/database/database.go)).
- SIGTERM: `/ready` menjadi 503 dan server tetap melayani 5 detik, request berjalan
  mendapat 15 detik, lalu email dan audit mendapat 5 detik untuk flush — pakai
  `terminationGracePeriodSeconds` ≥ 30.
- Migration berjalan di Job terpisah sebelum rollout (`SKIP_MIGRATION=true` di
  Deployment) — lihat [deployments/README.md](../deployments/README.md).
