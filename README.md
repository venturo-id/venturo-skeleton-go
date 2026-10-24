# Timebox Backend

Backend API untuk **Timebox** — fondasi autentikasi dan RBAC (role-based access control) tempat fitur-fitur Timebox dibangun. Dibuat dengan Go + Gin + PostgreSQL + Redis (+ MongoDB untuk audit log) sebagai modular monolith. Single-tenant: akses hanya ditentukan oleh role global, tanpa lapisan company/branch.

Yang sudah ada:

- **Auth** — signup, sign in (email/username + password, atau Google lewat OAuth langsung — authorization code + PKCE, untuk aplikasi desktop maupun web), access token JWT berumur pendek + refresh token yang dirotasi (di body untuk aplikasi desktop, di cookie httpOnly `tb_refresh` untuk aplikasi web), logout satu sesi atau semua sesi, lockout setelah password salah berulang, rate limit per IP. Verifikasi email dan reset password dengan kode 6 digit (OTP) di email; akun yang emailnya belum terverifikasi bisa diambil alih pemilik asli email itu (Google sign-in atau reset password). Setiap error membawa `code` yang stabil untuk FE.
- **Akun sendiri** — daftar sesi/perangkat aktif dan akhiri satu sesi, ganti email (kode konfirmasi ke alamat baru), link / unlink akun Google, ekspor data, hapus akun (data pribadi dihapus). Aksi sensitif minta password lagi atau sign in Google yang baru.
- **User** — CRUD user oleh admin (termasuk assign role, hanya untuk user yang emailnya terverifikasi), profil (termasuk zona waktu), foto profil (upload ke Cloudflare R2), dan ganti password milik sendiri.
- **Zona waktu per user** — nama IANA (`Asia/Jakarta` default, `America/New_York`, …). Semua waktu disimpan UTC; zona ini dipakai untuk menampilkan dan mengelompokkan tanggal.
- **Audit log** — sign in, perubahan kredensial/sesi, dan perubahan user/role/API key dicatat ke MongoDB (`GET /audit-logs`, permission `audit_logs:read`). Opsional: tanpa `MONGO_URI` tidak ada yang dicatat.
- **Role** — role global dengan permission per resource (`viewer` / `editor` / `admin`). Caller hanya bisa memberi permission yang ia pegang sendiri.
- **API key** — akses programatik milik user, dengan scope permission, IP whitelist, rate limit, dan masa berlaku.

Kontrak API ada di [docs/api-contract/README.md](docs/api-contract/README.md), ERD di [docs/erd/README.md](docs/erd/README.md).

## Prasyarat

- Go 1.26.10+ (versi lebih lama otomatis mengunduh toolchain 1.26.10)
- PostgreSQL 14+
- Redis — wajib, API tidak mau start tanpa Redis (cache permission & rate limit)
- MongoDB 6+ — opsional, untuk audit log (`MONGO_URI`)
- [golang-migrate](https://github.com/golang-migrate/migrate) CLI — untuk `make migrate-*` dan `make db-setup`
- [air](https://github.com/air-verse/air) — untuk hot reload (`make dev`)

### Install golang-migrate & air

```bash
# golang-migrate — macOS
brew install golang-migrate

# golang-migrate — Linux
curl -L https://github.com/golang-migrate/migrate/releases/download/v4.18.1/migrate.linux-amd64.tar.gz | tar xvz
sudo mv migrate /usr/local/bin/

# golang-migrate — lewat Go (semua OS; versi yang sama dengan image Docker)
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.1

# air — `make dev` menjalankan ~/go/bin/air
go install github.com/air-verse/air@latest
```

## Setup

1. Salin konfigurasi:

   ```bash
   cp .env.example .env
   ```

2. Isi `.env` (komentar lengkap ada di [.env.example](.env.example)):

   - `JWT_SECRET` — **wajib**, minimal 32 karakter. Buat dengan `openssl rand -base64 48`. API menolak start tanpanya.
   - `SEED_ADMIN_EMAIL`, `SEED_ADMIN_USERNAME`, `SEED_ADMIN_PASSWORD` — super admin pertama. Password 8–72 byte, ada huruf besar, huruf kecil, dan angka, dan tidak memuat nama email. Pakai email yang kamu kuasai: Google sign-in dengan email itu langsung masuk sebagai super admin.
   - `DB_*` dan `REDIS_*` — arahkan ke PostgreSQL dan Redis yang sedang jalan.
   - Opsional: `CORS_ALLOWED_ORIGINS` (tambahkan dev server FE, mis. `http://localhost:5173`; wajib untuk aplikasi web, termasuk refresh lewat cookie), `MONGO_URI` (audit log; kosong = tidak dicatat).
   - Opsional — Google sign-in: `GOOGLE_DESKTOP_CLIENT_ID` + `GOOGLE_DESKTOP_CLIENT_SECRET` (OAuth client tipe "Desktop app") dan/atau `GOOGLE_WEB_CLIENT_ID` + `GOOGLE_WEB_CLIENT_SECRET` + `GOOGLE_WEB_REDIRECT_URIS` (tipe "Web application", mis. `http://localhost:5173/oauth/google`). Platform yang kosong menjawab `503 google_not_configured`. Cara membuat client-nya: [deployments/README.md](deployments/README.md#google-oauth-client).
   - Opsional — foto profil: `R2_ACCOUNT_ID`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_BUCKET`, **dan** `R2_PUBLIC_URL` (URL publik bucket). Tanpa salah satunya upload foto menjawab `503 storage_not_configured` dan foto Google tidak disalin.
   - Opsional — email: tanpa SMTP, email (kode verifikasi, reset password, ganti email, dll.) tidak dikirim tapi di-log di level debug lengkap dengan kodenya — cukup untuk mencoba alurnya. Untuk melihat email sungguhan di development, pakai [Mailpit](https://mailpit.axllent.org) (lihat di bawah). Umur kode, batas tebakan, dan jeda kirim ulang diatur `OTP_EXPIRATION`, `OTP_MAX_ATTEMPTS`, `OTP_RESEND_COOLDOWN`; kuota email per akun `MAX_VERIFICATION_EMAILS`, `MAX_PASSWORD_RESET_EMAILS`.
   - `EMAIL_VERIFICATION_REQUIRED=true` (sign in ditolak sampai email terverifikasi) butuh SMTP — tanpanya API menolak start. Disarankan `true` di production begitu SMTP jalan.

   **Mailpit (SMTP lokal untuk development):**

   ```bash
   brew install mailpit
   brew services start mailpit   # SMTP di localhost:1025, UI di http://localhost:8025
   ```

   Lalu di `.env`:

   ```bash
   SMTP_HOST=localhost
   SMTP_PORT=1025
   SMTP_USER=
   SMTP_PASSWORD=
   SMTP_FROM_EMAIL=no-reply@timebox.id
   SMTP_NOTIFICATIONS_FROM_EMAIL=notifications@timebox.id
   ```

   Semua email yang dikirim API muncul di http://localhost:8025. SMTP aktif begitu `SMTP_HOST` dan `SMTP_FROM_EMAIL` terisi; `SMTP_USER` / `SMTP_PASSWORD` opsional tapi harus diisi berdua, dengan nilai asli (bukan base64). Email kode dikirim dari `SMTP_FROM_EMAIL`, email notifikasi dari `SMTP_NOTIFICATIONS_FROM_EMAIL` (kosong = ikut `SMTP_FROM_EMAIL`). Koneksi tanpa enkripsi hanya diizinkan ke host loopback (`localhost`, `127.0.0.0/8`, `::1`) dan tanpa kredensial — server SMTP lain wajib TLS (port `465`) atau STARTTLS.

   `make` membaca `.env` dengan `include`: tulis komentar di barisnya sendiri, `#` di dalam nilai sebagai `\#`, dan `$` sebagai `$$`.

3. Buat database sekali (nama sesuai `DB_NAME`, default `timebox`):

   ```bash
   createdb timebox
   ```

4. Jalankan migration lalu buat super admin:

   ```bash
   make db-setup
   ```

   Role sistem `super_admin` dan `administrator` dibuat oleh migration — tidak ada seeder SQL.

5. Jalankan server dengan hot reload:

   ```bash
   make dev
   ```

Cek server jalan:

```bash
curl http://localhost:8080/health   # liveness
curl http://localhost:8080/ready    # PostgreSQL + Redis terjangkau

curl -X POST http://localhost:8080/core/v1/auth/signin \
  -H "Content-Type: application/json" \
  -d '{"login": "<SEED_ADMIN_EMAIL>", "password": "<SEED_ADMIN_PASSWORD>"}'
```

## Perintah make

| Perintah | Fungsi |
|---|---|
| `make help` | Daftar semua perintah |
| `make dev` | Jalankan dengan hot reload (air) |
| `make run` | Jalankan tanpa hot reload |
| `make build` | Build binary ke `bin/timebox-api` |
| `make test` | Semua test dengan race detector (`go test -race ./...`) |
| `make test-integration` | Test integrasi (`-run Integration`) ke PostgreSQL sungguhan (`TEST_DATABASE_URL`, wajib) dan MongoDB (`TEST_MONGO_URI`, opsional) |
| `make lint` | Cek gofmt, `go vet`, dan staticcheck |
| `make fmt` | Format semua file Go |
| `make migrate-up` | Jalankan semua migration yang belum jalan |
| `make migrate-down MODULE=core` | Rollback satu migration terakhir |
| `make migrate-create NAME=create_foo MODULE=core` | Buat file migration baru |
| `make migrate-force V=7 MODULE=core` | Paksa versi migration (kalau dirty) |
| `make migrate-version` | Tampilkan versi migration |
| `make seed-admin` | Buat super admin dari `SEED_ADMIN_*` (aman diulang); `make seed` sama |
| `make db-setup` | `migrate-up` + `seed` |
| `make db-reset` | Drop & buat ulang database, lalu `db-setup`. Ditolak kalau `ENV=production` |

Migration yang gagal di-rollback tapi versinya ditandai dirty: perbaiki penyebabnya, `make migrate-force V=<versi sebelumnya> MODULE=core`, lalu `make migrate-up`.

`DB_*` yang tidak diisi (atau kosong) memakai default yang sama dengan API: `localhost:5432`, user dan password `postgres`, database `timebox`, `sslmode=disable`.

### Test integrasi

Test integrasi (alur kode email, reset password, klaim akun, Google sign-in, lockout, sesi, ganti email, hapus dan ekspor akun) jalan ke database yang sudah dimigrasi dan hanya aktif kalau `TEST_DATABASE_URL` diisi — tanpa itu test-nya di-skip. Test ini membuat dan menghapus user, jadi pakai database khusus test, jangan database yang datanya penting. Test audit log ke MongoDB aktif kalau `TEST_MONGO_URI` diisi; ia memakai lalu menghapus database acak miliknya sendiri:

```bash
createdb timebox_test
make migrate-up DB_NAME=timebox_test
TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/timebox_test?sslmode=disable' \
TEST_MONGO_URI='mongodb://localhost:27017' make test-integration
```

Jangan taruh `TEST_DATABASE_URL` / `TEST_MONGO_URI` di `.env`: `make` mengekspor isi `.env`, sehingga `make test` ikut menjalankan test integrasi.

## Struktur proyek

```
├── cmd/
│   ├── api/                  # Entry point API server
│   └── seed-admin/           # Membuat super admin pertama dari SEED_ADMIN_*
├── internal/
│   ├── config/               # Konfigurasi dari environment variable
│   ├── database/             # Connection pool, transaksi (WithTx), migrations/core
│   ├── middleware/           # JWTAuth, permission, rate limit, recovery
│   ├── modules/core/         # auth (+ /account), user, role, apikey, auditlog
│   ├── router/               # Wiring modul dan route
│   └── shared/               # audit, authz, mailer, ratelimit, redis, request, response
├── pkg/                      # crypto, jwt, token, validator, logger, discord,
│                             # googleauth, email, storage, avatar, types
├── deployments/              # Entrypoint Docker + panduan deployment
├── docs/                     # Kontrak API dan ERD
├── Dockerfile
└── Makefile
```

Setiap modul mengikuti pola `domain/`, `dto/`, `handler/`, `repository/`, `service/` + `main.<modul>.go`; konvensinya dijelaskan di [CLAUDE.md](CLAUDE.md).

## Logging

Memakai Zap dengan middleware ginzap:

- **Development**: output console berwarna, level debug
- **Production** (`ENV=production`): JSON, level info, tanpa sampling — setiap request tercatat, berapa pun trafiknya

Kalau `DISCORD_ALERT_WEBHOOK` diisi, log di level `DISCORD_ALERT_LEVEL` (`warn` kalau kosong, atau `error`) ke atas juga dikirim ke Discord.

## Dokumentasi

- [docs/api-contract/README.md](docs/api-contract/README.md) — format response, autentikasi, permission, dan kontrak tiap endpoint
- [docs/erd/README.md](docs/erd/README.md) — ERD skema `core`
- [deployments/README.md](deployments/README.md) — image Docker, entrypoint, dan kebutuhan deployment Kubernetes
- [CLAUDE.md](CLAUDE.md) — arsitektur dan konvensi kode
