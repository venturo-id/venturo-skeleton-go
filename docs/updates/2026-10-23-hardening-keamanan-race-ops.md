# Update 2026-10-23 — Keamanan, race condition, konfigurasi, dan ops

Hasil review menyeluruh setelah rilis self-service akun & audit log (commit `93188c7`). Perubahan ini **belum di-commit** saat catatan ini ditulis.

Detail kontrak API ada di [docs/api-contract](../api-contract/README.md) (bagian "⚠️ Behaviour Change 2026-10-23" di tiap dokumen); aturan untuk developer ada di [CLAUDE.md](../../CLAUDE.md); panduan deploy di [deployments/README.md](../../deployments/README.md).

---

## Ringkasan

| Area | Masalah | Sekarang |
|---|---|---|
| Ganti email sendiri | `PUT /users/:id` ke diri sendiri bisa mengganti email hanya dengan session — access token curian cukup untuk memindahkan akun (beserta role-nya) lalu mengklaimnya lewat forgot-password | `403`; harus lewat `POST /account/email` (minta password lagi + konfirmasi link) |
| Hapus / nonaktifkan diri sendiri | Bisa lewat `/users/:id`, termasuk super admin terakhir | `403`; pemilik menutup akun lewat `POST /account/delete` |
| Link ganti email | Tetap hidup setelah akun diklaim / password direset — pendaftar dengan email orang lain bisa merebut akun kembali | Ganti password, reset password, dan klaim akun membatalkan **semua** link yang belum dipakai |
| Sesi vs. reset / logout-all bersamaan | Refresh atau sign in yang berjalan bersamaan bisa meninggalkan sesi hidup | Diserialisasi per user (row lock); sesi yang terbit bersamaan ikut diakhiri |
| Update user / role bersamaan | Update menulis ulang seluruh baris dari data lama: user yang baru dinonaktifkan bisa aktif lagi, permission yang baru dipersempit bisa kembali | User dibaca dengan lock di dalam transaksi; role hanya menulis field yang dikirim |
| Google sign-in | Identity yang sudah ter-link mengabaikan `EMAIL_VERIFICATION_REQUIRED` | `401 Email not verified`, sama seperti sign in password |
| Refresh gagal di tengah jalan | Refresh token terlanjur terpakai; retry setelah 30 detik dianggap pencurian (semua sesi diakhiri) | Rotasi dibatalkan, token lama tetap berlaku untuk retry |
| Konfigurasi | Nilai env yang salah diam-diam diganti default (mis. `EMAIL_VERIFICATION_REQUIRED=yes` → `false`) | API menolak start dan menyebut semua nilai yang salah |
| Migration 000010 | Kolom sesi `NOT NULL` tanpa default → pod versi lama gagal sign in / refresh saat rolling update atau rollback | Migration `000011` menambah default |
| Email | Kirim SMTP tanpa batas waktu; server SMTP macet menahan semua slot selamanya | Batas 30 detik per kiriman, antrean maksimal 256 |
| Shutdown | Langsung berhenti saat SIGTERM (502 saat rollout); antrean email / audit bisa hilang kalau shutdown kelamaan | Drain 5 detik, lalu request 15 detik, lalu flush email & audit 5 detik |

---

## Detail perubahan

### 1. Keamanan

- **`PUT /users/:id` ke diri sendiri:**
  - `email` baru → `403 Cannot change your own email here` (detail mengarahkan ke `POST /core/v1/account/email`). Mengirim email yang sama dengan sekarang tetap boleh.
  - `is_active: false` → `403 Cannot deactivate or delete your own account`.
- **`DELETE /users/:id` ke diri sendiri** → `403 Cannot deactivate or delete your own account`.
- **Link email yang belum dipakai batal** (verifikasi, reset, ganti email) saat:
  - password diganti (`PUT /users/me/password`),
  - password direset (`POST /auth/reset-password`),
  - akun diklaim lewat Google sign-in.
- **Re-autentikasi dicek ulang saat perubahan dicatat:** kalau password atau email akun berubah di antara cek password dan pencatatan (mis. reset berjalan bersamaan), `POST /account/email` dan `POST /account/delete` menjawab `400 Confirm it is you`.
- **Google sign-in** lewat identity yang sudah ter-link mengikuti `EMAIL_VERIFICATION_REQUIRED`.
- **Toleransi selisih jam 5 detik** saat memvalidasi access token (`nbf` / `iat` / `exp`).
- **Log email tidak lagi memuat alamat penerima** — log error diteruskan ke Discord, dan alamat email adalah data pribadi.

### 2. Race condition

- Sign in (password & Google), refresh, dan "akhiri sesi" mengunci baris user di transaksi yang menerbitkan / mencabut token. Logout-all, reset password, ganti password, dan klaim akun menunggu kunci itu, lalu mencabut token yang baru terbit.
- Sign in yang sudah mencocokkan password lama kalah kalau password direset sebelum sesinya tercatat (`401 Invalid credentials`).
- Pengecekan terikat pada data yang dibaca: hash password (sign in, ganti password, re-autentikasi) dan alamat email (reset password).
- Refresh: klaim token lama, token baru, dan access token baru dibuat dalam satu transaksi.
- `PUT /users/:id`: baris user dibaca dengan lock di dalam transaksi.
- `PUT /roles/:id` dan `PUT /roles/:id/permissions`: hanya field yang dikirim yang ditulis; response berisi role seperti tersimpan.
- Dua permintaan link email bersamaan tidak lagi menghasilkan dua link yang sama-sama berlaku.

### 3. Konfigurasi ketat

`config.Load` menolak start (exit 1, pesan `invalid configuration: …` yang menyebut semua nilai salah sekaligus) untuk:

- boolean yang bukan `true` / `false` (mis. `EMAIL_VERIFICATION_REQUIRED=yes`),
- durasi tanpa satuan atau negatif (mis. `ACCOUNT_LOCKOUT_DURATION=30` — harus `30m`), termasuk `JWT_EXPIRATION`,
- angka yang bukan bilangan bulat (mis. `REDIS_DB=one`),
- `ENV` selain `development` / `staging` / `production`,
- `DB_SSLMODE` selain `disable` / `require` / `verify-ca` / `verify-full` (`prefer` / `allow` ditolak CLI `migrate`).

Tidak ada key env baru.

### 4. Database

- **Migration `000011_refresh_token_defaults`:**
  - `refresh_tokens.session_id DEFAULT gen_random_uuid()`, `signed_in_at DEFAULT now()` — pod versi sebelum `000010` tetap bisa sign in / refresh selama rollout atau rollback.
  - Index `expires_at` jadi index biasa (pembersihan token per jam tidak lagi full scan); index `revoked_at` yang tidak dipakai dihapus.
- **Aturan migration baru (expand/contract):** migration harus tetap bisa dipakai rilis sebelumnya — kolom `NOT NULL` baru wajib punya `DEFAULT`; kolom yang masih dibaca rilis sebelumnya tidak di-rename / di-drop di rilis yang sama.
- **Setiap koneksi API:** `application_name=timebox-api`, `statement_timeout` 30 detik, `idle_in_transaction_session_timeout` 60 detik.

### 5. Ops & deploy

- **SIGTERM:** `/ready` → `503` dan server tetap melayani 5 detik (supaya ingress berhenti mengirim request), lalu request yang berjalan diberi 15 detik, lalu email dan audit log di-flush 5 detik. Total 25 detik. Ctrl+C melewati jeda 5 detik; sinyal kedua langsung menghentikan proses.
- **`/ready`:** tetap `200` saat connection pool penuh karena traffic (supaya lonjakan beban tidak mengeluarkan semua replica sekaligus); tetap `503` kalau PostgreSQL / Redis benar-benar tidak terjangkau.
- **Email:** setiap kiriman SMTP dibatasi 30 detik (dial, TLS / STARTTLS, AUTH, DATA). Maksimal 8 terkirim bersamaan dan 256 antre; lebih dari itu dibuang dan dicatat di log.
- **Audit log:** entri yang dicatat setelah shutdown dimulai dibuang (dicatat sekali di log).
- **Migration di Kubernetes:** jalankan hanya di Job sebelum rollout; Deployment API memakai `SKIP_MIGRATION=true`. Entrypoint menunggu lock migration replica lain sampai 300 detik (sebelumnya default `migrate` 15 detik → crash-loop).
- **Dockerfile:** `USER 1000:1000` (numerik, supaya `runAsNonRoot` tanpa `runAsUser` bisa diverifikasi kubelet). `deploy.env` masuk `.gitignore` dan `.dockerignore`.
- **Makefile:** `migrate-*` tanpa `.env` memakai default yang sama dengan `config.go` (sebelumnya `sslmode=` kosong).
- **Log production tidak di-sampling** (sebelumnya ~99% access log hilang di atas 100 request/detik).

---

## Yang perlu dilakukan

### Frontend

- Tangani `403` baru di `PUT /users/:id` dan `DELETE /users/:id` untuk akun sendiri — arahkan ke halaman ganti email (`POST /account/email`) atau hapus akun (`POST /account/delete`).
- Link ganti email bisa batal setelah password diganti / direset — tampilkan pesan "link tidak berlaku, minta ulang".
- `400 Confirm it is you` di `/account/email` dan `/account/delete` bisa juga berarti password berubah di tengah jalan — minta user mengulang dengan password sekarang.
- Refresh yang gagal `500` boleh diulang dengan refresh token yang sama.
- Google sign-in bisa menjawab `401 Email not verified` saat `EMAIL_VERIFICATION_REQUIRED=true`.

### Deploy (Jenkins / Kubernetes)

1. Jalankan migration `000009`, `000010`, dan `000011` lewat Job **sebelum** rollout (`kubectl wait` sampai Job selesai).
2. Set `SKIP_MIGRATION=true` di Deployment API.
3. `terminationGracePeriodSeconds` ≥ 30.
4. Readiness probe: `timeoutSeconds` di atas 2 detik.
5. Cek nilai env di ConfigMap / Secret: nilai yang tidak valid sekarang membuat pod langsung exit (lihat log `invalid configuration: …`).
6. Kalau memakai PgBouncer: tambahkan `statement_timeout,idle_in_transaction_session_timeout` ke `ignore_startup_parameters` dan pasang nilainya di role database (lihat [deployments/README.md](../../deployments/README.md)).

---

## Verifikasi

- `make lint` (gofmt, go vet, staticcheck) lulus.
- `go test -race ./...` lulus.
- Integration test terhadap PostgreSQL dan MongoDB sungguhan lulus, termasuk test baru yang memaksa race secara deterministik (satu request ditahan di dalam transaksinya sementara request lain berjalan):
  - refresh vs. logout-all dan vs. akhiri sesi,
  - sign in vs. reset password,
  - update user vs. nonaktifkan user,
  - update role vs. mempersempit permission,
  - link ganti email vs. reset / klaim Google / ganti password.
- **Mutation test:** 16 proteksi baru masing-masing sengaja dirusak — semuanya membuat test gagal.
- Migration `000011` diuji naik → turun → naik; insert gaya rilis lama (tanpa kolom sesi) berhasil.
- Smoke test HTTP pada binary asli: `/ready`, sign in, `403` untuk email / status / hapus diri sendiri, refresh, logout-all, konfigurasi salah ditolak, SIGTERM (`/ready` 503 dan `/health` 200 selama drain, berhenti bersih setelah 5 detik).
- `docker build .` berhasil; image berjalan sebagai uid 1000.

---

## Belum dikerjakan

- **Sisa risiko:** dua super admin yang saling menghapus pada saat yang persis sama.
- **Keamanan:**
  - membuat API key masih cukup dengan session — pertimbangkan re-autentikasi atau masa berlaku default;
  - sesama admin bisa mengambil alih akun admin lain lewat ganti email (email yang diganti admin langsung terverifikasi);
  - belum ada rate limit per user untuk `/account/*` (terutama `GET /export`);
  - belum ada entri audit khusus saat akun Google di-link ke akun yang sudah ada;
  - password salah pada akun Google-only tetap dihitung, sehingga orang lain bisa memicu email "akun terkunci".
- **UU PDP:** user yang dihapus admin menyimpan data pribadinya selamanya — perlu job retensi.
- **Ops:**
  - Jenkinsfile dan manifest Kubernetes;
  - request ID di log dan audit, serta metrics;
  - outbox email di PostgreSQL supaya email tidak hilang saat pod crash;
  - PgBouncer < 1.21 dalam transaction mode butuh statement cache pgx dimatikan (perlu perubahan kode).

---

## File utama yang berubah

| Area | File |
|---|---|
| Auth | `internal/modules/core/auth/service/{auth.service,account,reauth,email_change,close_account,sessions}.go`, `internal/modules/core/auth/repository/account_token.repository.go` |
| User | `internal/modules/core/user/{service/user.service,repository/user.repository,handler/user.handler}.go` |
| Role | `internal/modules/core/role/{service/role.service,repository/role.repository}.go` |
| Config & JWT | `internal/config/config.go`, `pkg/jwt/jwt.go`, `cmd/api/main.go`, `cmd/seed-admin/main.go` |
| Database | `internal/database/database.go`, `internal/database/migrations/core/000011_refresh_token_defaults.{up,down}.sql` |
| Ops | `internal/router/{router,ready}.go`, `internal/shared/mailer/async.go`, `internal/shared/audit/mongo.go`, `pkg/email/{smtp,transport}.go`, `pkg/logger/logger.go`, `Dockerfile`, `deployments/docker/docker-entrypoint.sh`, `Makefile` |
| Test baru | `internal/modules/core/auth/service/race_integration_test.go`, `internal/modules/core/user/service/user_integration_test.go`, `internal/modules/core/role/service/role_integration_test.go`, plus test untuk readiness, transport SMTP, mailer, audit store, database, logger |
| Dokumen | `CLAUDE.md`, `README.md`, `deployments/README.md`, `docs/api-contract/**`, `docs/erd/*` |
