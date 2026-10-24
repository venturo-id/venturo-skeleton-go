# Deployment

Timebox Backend dikirim sebagai satu image Docker. Repo ini tidak menyimpan manifest Kubernetes, pipeline CI, maupun docker-compose — bagian [Kubernetes](#kubernetes-yang-dibutuhkan) merangkum apa yang perlu disiapkan di cluster.

## Struktur folder

```
deployments/
├── README.md                    # Dokumen ini
└── docker/
    └── docker-entrypoint.sh     # Entrypoint container: tunggu DB → migration → jalankan command

# Di root repo:
Dockerfile                       # Definisi image
```

## Image

[`Dockerfile`](../Dockerfile), multi-stage:

| | |
|---|---|
| Builder | `golang:1.26.10-alpine` (sama dengan `go` di go.mod); golang-migrate v4.18.1 di-build dari source (driver `postgres`) |
| Runtime | `alpine:3.22` + `ca-certificates`, `tzdata`, `postgresql-client`, `bash` |
| Binary | `/app/api` (command default) dan `/app/seed-admin` |
| Migration | `/app/internal/database/migrations` |
| User | non-root `appuser`, ditulis numerik `USER 1000:1000` — `runAsNonRoot: true` lolos tanpa `runAsUser` |
| Port | `8080` (`EXPOSE 8080`, `SERVER_PORT=8080`) |
| Env bawaan | `ENV=production` (log JSON, gin release mode), `TZ=UTC` |
| `HEALTHCHECK` | `GET http://127.0.0.1:8080/health` tiap 30 detik |

Build dan jalankan lokal:

```bash
docker build -t timebox-be .
docker run --rm -p 8080:8080 --env-file deploy.env timebox-be
```

Jangan langsung memakai `.env` development sebagai `--env-file`: di dalam container `DB_HOST=localhost` menunjuk ke container itu sendiri, dan escape `\#` / `$$` di `.env` hanya berlaku untuk `make` — Docker membacanya apa adanya. `deploy.env` sudah ada di `.gitignore` dan `.dockerignore`, jadi tidak ikut ter-commit maupun masuk ke image.

## Entrypoint

[`docker/docker-entrypoint.sh`](docker/docker-entrypoint.sh) dijalankan setiap container start:

1. Kalau `SKIP_MIGRATION` bukan `true`:
   - tunggu database bisa dihubungi (`psql`, maksimal 5 menit; lewat dari itu → exit 1),
   - jalankan migration core (`migrate up -lock-timeout 300`, tabel versi `schema_migrations_core`); gagal → exit 1.
2. `exec` command container — default `/app/api`.

`migrate` memegang advisory lock PostgreSQL selama bermigrasi. Container lain yang start bersamaan menunggu lock itu sampai 5 menit (`-lock-timeout 300`; default `migrate` hanya 15 detik, setelah itu container exit), lalu tidak menemukan migration yang tersisa. Meski begitu, di Kubernetes jalankan migration **hanya di Job** — lihat [Migration](#migration-job-sebelum-rollout). Password database dikirim lewat `PGPASSWORD`, tidak pernah lewat URL.

### Shutdown

Saat menerima SIGTERM (Kubernetes, `docker stop`), API berhenti bertahap — paling lama 25 detik:

1. **Drain, 5 detik**: `/ready` langsung menjawab `503`, tapi request tetap dilayani — ingress masih mengirim request ke pod yang sedang berhenti sampai ia sadar endpoint-nya hilang, dan tanpa jeda ini request itu berakhir `502` di setiap rollout. `/health` tetap `200` sampai proses keluar.
2. **Request berjalan, maksimal 15 detik**: server berhenti menerima koneksi baru dan menunggu request yang sedang berjalan. Yang belum selesai lewat dari itu diputus (log `Shutdown: in-flight requests did not finish in time`).
3. **Flush, maksimal 5 detik**: email yang masih antre (mis. kode reset password) dikirim dan entri audit log yang belum ditulis disimpan ke MongoDB, berdampingan — juga ketika langkah 2 kehabisan waktu. Entri audit dari request yang masih berjalan setelah ini dibuang (log `Audit log is closed; entries dropped`).

SIGINT (Ctrl+C) melewati langkah 1. Sinyal kedua menghentikan proses saat itu juga.

## Environment variables

Daftar lengkap beserta komentarnya: [`.env.example`](../.env.example).

### Wajib

| Variable | Keterangan |
|---|---|
| `JWT_SECRET` | ≥ 32 karakter dan bukan placeholder yang pernah ada di repo. Tanpa ini API tidak start. Juga kunci (turunan) hash kode email — lihat [Praktik keamanan](#praktik-keamanan) |
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME` | PostgreSQL |
| `DB_SSLMODE` | Default `disable` — set `require` (atau `verify-ca` / `verify-full`) di production. Jangan `prefer` / `allow`: API (pgx) menerimanya, tapi `migrate` (lib/pq) di entrypoint dan Job menolaknya |
| `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD`, `REDIS_DB` | Redis (cache authz, rate limit, jeda kirim ulang dan kuota kode email). Tanpa Redis API tidak start. Semua key berawalan `timebox:` |
| `TRUSTED_PROXIES` | IP/CIDR yang boleh mengisi `X-Forwarded-For`, mis. CIDR pod ingress controller (`10.42.0.0/16` di RKE). Kosong di belakang ingress = semua client berbagi IP ingress untuk rate limit dan IP whitelist API key |
| `CORS_ALLOWED_ORIGINS` | Origin FE web dipisah koma, mis. `https://app.timebox.id,https://timebox.id` (dev: `http://localhost:5173,http://localhost:5174`). Kosong = browser dari origin lain tidak bisa memanggil API. CORS mengizinkan header `Authorization`, `X-API-Key`, dan `X-Timebox-Client` dengan credentials; refresh lewat cookie `tb_refresh` hanya diterima dari origin di daftar ini. Aplikasi desktop (Electron, tanpa `Origin`) tidak terpengaruh |

### Opsional

| Variable | Default | Keterangan |
|---|---|---|
| `JWT_EXPIRATION` | `15m` | Umur access token (juga `expires_in`) |
| `JWT_REFRESH_EXPIRATION` | `168h` | Umur refresh token, juga `Max-Age` cookie `tb_refresh` |
| `AUTH_RATE_LIMIT` / `AUTH_RATE_LIMIT_WINDOW` | `20` / `1m` | Per IP client untuk setiap endpoint `/auth` publik: `signup`, `signin`, `google/client`, `google`, `refresh`, `verify-email`, `verify-email/resend`, `forgot-password`, `reset-password/verify`, `reset-password` (bucket terpisah) |
| `MAX_LOGIN_ATTEMPTS` / `ACCOUNT_LOCKOUT_DURATION` | `5` / `30m` | Password salah berturut-turut sebelum akun dikunci, dan lamanya |
| `EMAIL_VERIFICATION_REQUIRED` | `false` | `true` = sign in ditolak sampai email terverifikasi. Butuh SMTP — tanpa SMTP API menolak start. Disarankan `true` di production begitu SMTP jalan |
| `OTP_EXPIRATION` | `15m` | Umur kode 6 digit di email (verifikasi, reset password, ganti email) |
| `OTP_MAX_ATTEMPTS` | `5` | Tebakan salah sebelum kode mati. Minimal `1` (selain itu API menolak start) |
| `OTP_RESEND_COOLDOWN` | `1m` | Jeda minimum antar-kode ke alamat yang sama (ganti email: ke akun yang sama), juga untuk alamat yang tidak terdaftar |
| `RESET_TOKEN_EXPIRATION` | `10m` | Umur `reset_token` hasil kode reset yang benar |
| `MAX_VERIFICATION_EMAILS` / `MAX_PASSWORD_RESET_EMAILS` | `5` / `3` | Kode verifikasi / reset per akun per jam (counter di Redis). Lewat dari itu endpoint menjawab sama tapi tidak mengirim. `MAX_VERIFICATION_EMAILS` juga membatasi kode ganti email (`429 email_quota_exceeded`) |
| `REDIS_PERMISSION_TTL` | `10m` | Lama cache state authz per user |
| `GOOGLE_DESKTOP_CLIENT_ID`, `GOOGLE_DESKTOP_CLIENT_SECRET` | kosong | OAuth client Google tipe **Desktop app** untuk aplikasi Electron (redirect loopback `http://127.0.0.1:<port>/…`). Diisi berdua atau tidak sama sekali. Kosong = Google sign-in desktop (juga re-autentikasi dan link Google dari desktop) menjawab `503 google_not_configured`. Lihat [Google OAuth client](#google-oauth-client) |
| `GOOGLE_WEB_CLIENT_ID`, `GOOGLE_WEB_CLIENT_SECRET` | kosong | OAuth client Google tipe **Web application** untuk aplikasi web. Diisi berdua; butuh `GOOGLE_WEB_REDIRECT_URIS` |
| `GOOGLE_WEB_REDIRECT_URIS` | kosong | Redirect URI client web, dipisah koma, **persis sama** dengan yang terdaftar di Google, mis. `https://app.timebox.id/oauth/google`. Wajib `https` (kecuali `http://localhost`, `127.0.0.1`, `[::1]`). Dikirim ke FE lewat `GET /auth/google/client?platform=web`; `redirect_uri` lain ditolak `400 invalid_redirect_uri` |
| `R2_ACCOUNT_ID`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_BUCKET` | kosong | Cloudflare R2 untuk foto profil. Storage aktif begitu keempatnya terisi |
| `R2_PUBLIC_URL` | kosong | URL publik bucket (custom domain), mis. `https://cdn.timebox.id` — origin semua `avatar_url` (`<R2_PUBLIC_URL>/avatars/<user_id>/<id>.jpg`). **Foto profil butuh R2 dan `R2_PUBLIC_URL`**: tanpa salah satunya `PUT` / `DELETE /users/me/avatar` menjawab `503 storage_not_configured` dan foto Google tidak disalin. CSP `img-src` aplikasi desktop (dan web) harus mengizinkan origin ini. Lihat [R2 custom domain untuk avatar](#r2-custom-domain-untuk-avatar) |
| `MONGO_URI` | kosong | Audit log di MongoDB. Kosong = tidak ada yang dicatat dan `GET /audit-logs` menjawab 503. Kalau diisi, MongoDB harus menjawab saat start (kalau tidak, API menolak start); gangguan setelahnya hanya membuat entri dibuang (tercatat di log error), request tidak ikut gagal. Kredensial ada di URI, jadi simpan di Secret |
| `MONGO_DATABASE` / `AUDIT_LOG_RETENTION` | `timebox` / `8760h` | Database koleksi `audit_logs`, dan lama entri disimpan (TTL index; perubahan berlaku di start berikutnya) |
| `DISCORD_ALERT_WEBHOOK`, `DISCORD_ALERT_LEVEL`, `DISCORD_ALERT_DEBOUNCE`, `DISCORD_ALERT_SOURCE` | kosong / `warn` / `10s` / hostname | Kirim log ke webhook Discord. `DISCORD_ALERT_LEVEL` kosong (atau tidak dikenal) = `warn`; isi `error` supaya warning tidak ikut terkirim (`.env.example` mengisinya `error`) |
| `DB_MAX_CONNS`, `DB_MIN_CONNS`, … | lihat `.env.example` | Connection pool per pod (default maksimal 25 koneksi) — lihat [Koneksi PostgreSQL](#koneksi-postgresql) |
| `SKIP_MIGRATION` | `false` | Hanya dibaca entrypoint |

Dihapus sejak 2026-10-24 (tidak dibaca lagi, boleh dibuang dari ConfigMap / Secret): `FIREBASE_PROJECT_ID`, `FIREBASE_CREDENTIALS_JSON`, `EMAIL_VERIFICATION_URL`, `RESET_PASSWORD_URL`, `EMAIL_CHANGE_URL`, `EMAIL_VERIFICATION_EXPIRATION`, `PASSWORD_RESET_EXPIRATION`.

### Email (SMTP) — perlu di production

Signup, verifikasi email, lupa/reset password, ganti email, lock akun, ganti password, link / unlink akun Google, dan hapus akun mengirim email. Tanpa SMTP API tetap start (kecuali `EMAIL_VERIFICATION_REQUIRED=true`), tapi email hanya di-log di level debug — yang di production (`ENV=production`, level info) tidak tercatat — jadi user tidak pernah menerima kode verifikasi maupun reset password.

| Variable | Default | Keterangan |
|---|---|---|
| `SMTP_HOST`, `SMTP_FROM_EMAIL` | kosong | Pengiriman aktif begitu keduanya terisi. `SMTP_FROM_EMAIL` mengirim email kode (verifikasi, reset password, ganti email); alamat polos tanpa nama, mis. `no-reply@timebox.id` |
| `SMTP_NOTIFICATIONS_FROM_EMAIL` | kosong (= `SMTP_FROM_EMAIL`) | Pengirim email notifikasi: welcome, akun terkunci, password / email diganti, akun ditutup, Google tertaut / dilepas. Alamat polos, mis. `notifications@timebox.id` |
| `SMTP_USER`, `SMTP_PASSWORD` | kosong | Opsional, tapi diisi berdua atau tidak sama sekali (satu saja → API menolak start). Tanpa keduanya API tidak melakukan AUTH. Isi nilai aslinya, bukan base64 — AUTH meng-encode sendiri, nilai yang sudah di-encode ditolak `535` |
| `SMTP_PORT` | `587` | STARTTLS; `465` = TLS sejak awal. Port lain juga wajib STARTTLS |
| `SMTP_FROM_NAME`, `APP_NAME` | kosong / `Timebox` | Nama pengirim (kosong = `APP_NAME`); `APP_NAME` muncul di subject dan isi email |
| `FRONTEND_URL` | `https://app.timebox.id` | Tujuan tombol di email welcome. Harus URL `http(s)` absolut |
| `SUPPORT_EMAIL` | kosong | Alamat kontak di footer email; kosong = baris kontak tidak ditampilkan |

Koneksi selalu terenkripsi (TLS di port 465, STARTTLS di port lain); server yang tidak menawarkan STARTTLS ditolak saat kirim (log `Email not sent`). Satu-satunya pengecualian: host loopback (`localhost`, `127.0.0.0/8`, `::1`) **tanpa** `SMTP_USER` / `SMTP_PASSWORD` — untuk server uji lokal seperti Mailpit (`SMTP_HOST=localhost`, `SMTP_PORT=1025`). Kredensial tidak pernah dikirim tanpa enkripsi.

SMTP yang diisi tapi tidak valid (`SMTP_FROM_EMAIL` / `SMTP_NOTIFICATIONS_FROM_EMAIL` bukan alamat polos, `FRONTEND_URL` bukan URL `http(s)` absolut, hanya salah satu dari `SMTP_USER` / `SMTP_PASSWORD`, `SMTP_PORT` di luar 1–65535) membuat API menolak start. Host atau password SMTP yang salah baru ketahuan saat email pertama dikirim: log `Email not sent`.

`SEED_ADMIN_*` hanya dibaca `/app/seed-admin`, bukan oleh API. Jangan dipasang permanen di Deployment.

### Google OAuth client

Google sign-in memakai OAuth client milik Timebox sendiri (tanpa Firebase): aplikasi menjalankan authorization code flow + PKCE, BE menukar code dengan client secret. Satu client per platform, dibuat di [Google Cloud Console](https://console.cloud.google.com/) pada project Timebox:

1. **OAuth consent screen** (Google Auth Platform → Branding / Audience / Data access): nama aplikasi, email support, authorized domain `timebox.id`; audience **External**; scope `openid`, `.../auth/userinfo.email`, `.../auth/userinfo.profile` (non-sensitive — tidak butuh verifikasi Google). Publish ke *In production*; selama *Testing* hanya test user yang bisa sign in.
2. **Client desktop** — Credentials → Create credentials → OAuth client ID → Application type **Desktop app** (mis. `Timebox Desktop`). Tidak ada redirect URI yang perlu didaftarkan: client tipe ini menerima redirect loopback `http://127.0.0.1:<port>` di port mana pun. Salin client ID / secret ke `GOOGLE_DESKTOP_CLIENT_ID` / `GOOGLE_DESKTOP_CLIENT_SECRET`. Secret client desktop tidak dianggap rahasia oleh Google, tapi tetap simpan di Secret.
3. **Client web** — Create credentials → OAuth client ID → Application type **Web application** (mis. `Timebox Web`), **Authorized redirect URIs**:
   - production: `https://app.timebox.id/oauth/google`
   - development: `http://localhost:5173/oauth/google`, `http://localhost:5174/oauth/google`

   Authorized JavaScript origins tidak dibutuhkan (penukaran code terjadi di BE). Salin ke `GOOGLE_WEB_CLIENT_ID` / `GOOGLE_WEB_CLIENT_SECRET`, dan isi `GOOGLE_WEB_REDIRECT_URIS` dengan redirect URI environment itu, persis sama (mis. production `https://app.timebox.id/oauth/google`; development `http://localhost:5173/oauth/google,http://localhost:5174/oauth/google`). Disarankan client web terpisah untuk production dan development.
4. Cek: log startup tidak lagi berisi `Google sign-in not configured` untuk platform itu, dan `GET /core/v1/auth/google/client?platform=web` (atau `desktop`) mengembalikan `client_id`.

Aplikasi meminta scope `openid email profile` dengan `code_challenge_method=S256`. Identity Google disimpan per id akun Google (`sub`); migration `000012` memindahkan identity lama dari uid Firebase ke `sub`-nya.

### R2 custom domain untuk avatar

Foto profil (upload user dan salinan foto Google) disimpan di bucket R2 sebagai `avatars/<user_id>/<uuid>.jpg` dan dibaca aplikasi langsung dari domain publik bucket:

1. Buat bucket (mis. `timebox-files`) → isi `R2_BUCKET`; account ID Cloudflare → `R2_ACCOUNT_ID`.
2. R2 → Manage API tokens → token **Object Read & Write** yang dibatasi ke bucket itu → `R2_ACCESS_KEY_ID` / `R2_SECRET_ACCESS_KEY` (simpan di Secret).
3. Bucket → Settings → Public access → **Custom Domains** → hubungkan domain di zona Cloudflare yang sama, mis. `cdn.timebox.id` → `R2_PUBLIC_URL=https://cdn.timebox.id`. Jangan pakai URL `r2.dev` di production (dibatasi rate-nya dan tanpa cache Cloudflare).
4. Tambahkan origin itu ke CSP `img-src` aplikasi desktop (dan web), mis. `img-src 'self' https://cdn.timebox.id`.

Setiap upload mendapat key baru, jadi isi URL tidak pernah berubah dan boleh di-cache selamanya. Mengganti `R2_PUBLIC_URL` belakangan tidak memindahkan `avatar_url` yang sudah tersimpan (masih menunjuk domain lama), dan file lama di bawah domain lama tidak lagi dihapus API saat diganti. Dengan R2 tapi tanpa `R2_PUBLIC_URL`, log startup `R2 storage or R2_PUBLIC_URL not configured — profile photos disabled`.

## Kubernetes: yang dibutuhkan

Tidak ada manifest di repo. Deployment API perlu:

- **Image** dari Dockerfile ini, container port `8080`.
- **Env** dari ConfigMap (nilai biasa) dan Secret (`JWT_SECRET`, `DB_PASSWORD`, `REDIS_PASSWORD`, `MONGO_URI`, `SMTP_PASSWORD`, `GOOGLE_DESKTOP_CLIENT_SECRET`, `GOOGLE_WEB_CLIENT_SECRET`, `R2_SECRET_ACCESS_KEY`, `DISCORD_ALERT_WEBHOOK`).
- **Probe**:
  - liveness → `GET /health` (statis, tidak menyentuh DB/Redis; tetap `200` selama shutdown)
  - readiness → `GET /ready`: `503` kalau PostgreSQL atau Redis tidak terjangkau (koneksi ditolak, autentikasi gagal, host tidak dikenal, tidak menjawab dalam 2 detik) atau pod sedang drain ([Shutdown](#shutdown)). Pool koneksi yang penuh terpakai request dihitung *siap* — kalau tidak, semua replica keluar dari service bersamaan tepat saat beban tertinggi. MongoDB sengaja tidak ikut dicek: audit log yang terganggu tidak boleh mengeluarkan pod dari service
  - readiness disarankan `periodSeconds: 5`, `timeoutSeconds: 3`, `failureThreshold: 3`. `timeoutSeconds` harus di atas batas 2 detik per pengecekan di dalam API; dengan default Kubernetes (1 detik) probe gagal duluan sebelum API sempat menilai pool yang sibuk
- **`SKIP_MIGRATION=true`** — migration dijalankan Job, bukan pod API ([Migration](#migration-job-sebelum-rollout)).
- **`terminationGracePeriodSeconds: 30`** (default Kubernetes) atau lebih — API butuh paling lama 25 detik (5 drain + 15 request + 5 flush, lihat [Shutdown](#shutdown)). Tidak perlu `preStop` sleep: API sudah drain sendiri. Kalau tetap memasang `preStop`, tambahkan durasinya ke grace period.
- **`TRUSTED_PROXIES`** berisi CIDR pod ingress controller.
- **Replica** boleh lebih dari satu: rate limit, jeda kirim ulang dan kuota kode email, dan cache authz ada di Redis, dan pembersihan refresh token, kode email, serta `reset_token` yang kedaluwarsa (tiap jam, di setiap pod) idempotent. Jumlah koneksi database ikut berlipat — lihat [Koneksi PostgreSQL](#koneksi-postgresql).

### Migration: Job sebelum rollout

Migration dijalankan **hanya** oleh Job, sekali per rilis, sebelum Deployment di-rollout:

1. Deployment API memakai `SKIP_MIGRATION=true`.
2. Pipeline (Jenkins) membuat Job dengan image rilis baru dan `args: ["true"]` — entrypoint menunggu database, menjalankan migration, lalu `exec true` dan selesai. Pakai `restartPolicy: Never`, `backoffLimit: 0` (migration yang gagal meninggalkan versi dirty; mengulang tidak membantu), nama unik per rilis (mis. `timebox-be-migrate-<build>`), dan `ttlSecondsAfterFinished` supaya Job lama terhapus.
3. Tunggu Job selesai: `kubectl wait --for=condition=complete job/<nama> --timeout=15m`. Gagal atau timeout → hentikan pipeline tanpa rollout, lalu baca `kubectl logs job/<nama>`.
4. Baru setelah itu ganti image Deployment.

Jangan bermigrasi saat pod API start: selama entrypoint menunggu database atau bermigrasi, API belum jalan dan `/health` belum menjawab, sehingga liveness probe bisa membunuh container di tengah migration (versi tertinggal dirty), migration yang lama membuat replica lain ikut menunggu lalu crash-loop, dan setiap pod baru di rolling update ikut mencoba.

**Expand/contract.** Selama Job berjalan, sepanjang rolling update, dan saat rollback, pod rilis sebelumnya tetap melayani traffic di atas skema yang baru. Jadi setiap migration harus membuat rilis sebelumnya tetap jalan:

- kolom baru yang `NOT NULL` wajib punya `DEFAULT` — kode lama tidak mengisinya;
- jangan rename atau drop kolom/tabel yang masih dibaca atau ditulis kode lama: tambahkan yang baru dan pindahkan kodenya dulu (expand), hapus yang lama di rilis berikutnya (contract);
- constraint baru (`CHECK`, `UNIQUE`, foreign key) harus menerima data yang masih ditulis kode lama.

`statement_timeout` API (30 detik) tidak berlaku untuk migration — `migrate` memakai koneksinya sendiri — tapi `ALTER TABLE` yang memegang lock lama membuat query API menunggu lalu gagal di 30 detik itu. Pecah backfill besar menjadi beberapa langkah dan jalankan di jam sepi.

### Koneksi PostgreSQL

Setiap pod membuka paling banyak `DB_MAX_CONNS` (default 25) koneksi. Pada jumlah pod terbanyak — replica ditambah `maxSurge` rolling update, atau batas atas HPA — totalnya harus di bawah `max_connections` PostgreSQL (default 100) dikurangi `superuser_reserved_connections` (default 3) dan klien lain (Job migration, `psql`, backup, monitoring):

```
(replica + maxSurge) × DB_MAX_CONNS  <  max_connections − cadangan
```

Contoh: 3 replica + `maxSurge` 1 dengan default 25 = 100 koneksi, sudah melewati default PostgreSQL. Turunkan `DB_MAX_CONNS` (mis. 20 → 80) atau naikkan `max_connections`.

Setiap koneksi dibuka dengan `application_name=timebox-api` (terlihat di `pg_stat_activity`), `statement_timeout` 30 detik (query yang lebih lama dibatalkan) dan `idle_in_transaction_session_timeout` 60 detik (transaksi yang menggantung diakhiri, lock-nya lepas). Nilainya konstanta di [`internal/database/database.go`](../internal/database/database.go).

Di belakang **PgBouncer mode transaction**:

- pgx menyimpan prepared statement per koneksi (cache statement, default pgx), yang tidak cocok dengan pooling per transaksi (`prepared statement "stmtcache_…" does not exist`) kecuali PgBouncer ≥ 1.21 dengan `max_prepared_statements` > 0. Untuk PgBouncer yang lebih lama cache itu harus dimatikan — `ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec` (atau `QueryExecModeCacheDescribe`) di `database.ParseConfig`; ini perubahan kode, belum ada env-nya.
- PgBouncer menolak startup parameter yang tidak ia kenal (`unsupported startup parameter: statement_timeout`). Tambahkan `statement_timeout,idle_in_transaction_session_timeout` ke `ignore_startup_parameters`, lalu pasang nilainya di role database: `ALTER ROLE <user> SET statement_timeout = '30s'; ALTER ROLE <user> SET idle_in_transaction_session_timeout = '60s';`.
- Hitungan di atas lalu berlaku untuk `max_client_conn` PgBouncer; ke PostgreSQL yang terpakai `default_pool_size`.

### Super admin pertama

Sekali, setelah migration selesai:

```bash
kubectl exec -it <pod-api> -- env \
  SEED_ADMIN_EMAIL=admin@timebox.id \
  SEED_ADMIN_USERNAME=superadmin \
  SEED_ADMIN_PASSWORD='<password-kuat>' \
  /app/seed-admin
```

- Password: 8–72 byte, huruf besar + huruf kecil + angka, tidak memuat nama email. Ganti setelah sign in pertama — dan karena perintah di atas masuk ke shell history, jangan pakai password itu untuk hal lain.
- Aman diulang: akun yang sudah ada hanya dipastikan memegang role `super_admin`, password-nya tidak diubah.
- Menolak akun yang sudah ada tapi emailnya belum terverifikasi (hasil signup sendiri) atau tidak aktif.

## Troubleshooting

### Container langsung exit

Lihat log (`kubectl logs <pod>` / `docker logs <container>`). Penyebab umum: `JWT_SECRET` kosong, terlalu pendek, atau placeholder; PostgreSQL atau Redis tidak terjangkau; `MONGO_URI` diisi tapi MongoDB tidak terjangkau atau kredensialnya salah; `TRUSTED_PROXIES` berisi nilai yang bukan IP/CIDR; `EMAIL_VERIFICATION_REQUIRED=true` tanpa SMTP, atau SMTP yang tidak valid (lihat [Email](#email-smtp--perlu-di-production)); R2 yang tidak valid saat `R2_PUBLIC_URL` diisi (log `initialize R2 storage: …`); nilai env yang tidak bisa dibaca — log `invalid configuration: …` menyebut semuanya sekaligus, mis. `EMAIL_VERIFICATION_REQUIRED="yes"`, durasi tanpa satuan (`30` bukan `30m`), `ENV` selain `development` / `staging` / `production`, atau `DB_SSLMODE` selain `disable` / `require` / `verify-ca` / `verify-full` (`prefer` / `allow` juga ditolak `migrate`), `OTP_MAX_ATTEMPTS` di bawah 1, `GOOGLE_*_CLIENT_ID` tanpa `GOOGLE_*_CLIENT_SECRET` atau sebaliknya (`… must be set together`), client web tanpa `GOOGLE_WEB_REDIRECT_URIS`, atau redirect URI web yang bukan `https` (`http` hanya untuk localhost); migration gagal, atau container lain memegang lock migration lebih dari 5 menit (`timeout: can't acquire database lock`).

### Migration gagal (versi dirty)

Migration yang gagal di-rollback, tapi versinya ditandai dirty dan `migrate up` berikutnya ikut gagal — begitu juga migration yang prosesnya dibunuh di tengah jalan (liveness probe, OOM). Perbaiki penyebabnya, paksa ke versi sebelumnya, lalu jalankan ulang (atau buat ulang Job-nya). Dari mesin yang bisa menjangkau database:

```bash
make migrate-force V=<versi-sebelumnya> MODULE=core
make migrate-up
```

Atau dari pod yang jalan dengan `SKIP_MIGRATION=true`:

```bash
kubectl exec -it <pod-api> -- sh -c 'PGPASSWORD="$DB_PASSWORD" migrate \
  -path /app/internal/database/migrations/core \
  -database "postgresql://$DB_USER@${DB_HOST:-localhost}:${DB_PORT:-5432}/$DB_NAME?sslmode=${DB_SSLMODE:-disable}&x-migrations-table=schema_migrations_core" \
  force <versi-sebelumnya>'
```

### Pod tidak pernah Ready

`/ready` menjawab `503` → PostgreSQL atau Redis tidak terjangkau dari pod. Log berisi `Readiness check failed` beserta error-nya. Tanpa log itu, pod sedang shutdown (drain) — `503` di situ memang disengaja.

### `502` saat rollout

API melayani 5 detik setelah SIGTERM sebelum berhenti ([Shutdown](#shutdown)). Kalau `502` masih muncul: pastikan `terminationGracePeriodSeconds` tidak lebih pendek dari 30 detik dan tidak ada `preStop` yang memakan grace period; ingress yang lebih lambat dari itu memperbarui endpoint butuh drain yang lebih lama (konstanta `drainDelay` di [`cmd/api/main.go`](../cmd/api/main.go)).

### Email tidak sampai

Log startup `SMTP is not configured — emails are only logged at debug level` → `SMTP_HOST` atau `SMTP_FROM_EMAIL` kosong. Log `Email not sent` (dengan `kind`) → SMTP menolak atau tidak menjawab (setiap pengiriman menyerah setelah 30 detik); cek host, port, user, dan password — termasuk `SMTP server does not offer STARTTLS; refusing to send unencrypted` (server tanpa TLS di host yang bukan loopback, atau dengan kredensial). Log `Email queue is full; emails dropped` → SMTP macet: 8 email dikirim bersamaan dan paling banyak 256 menunggu, selebihnya dibuang. Kalau terkirim tapi tidak sampai, cek folder spam dan konfigurasi SPF/DKIM domain `SMTP_FROM_EMAIL` dan `SMTP_NOTIFICATIONS_FROM_EMAIL`.

### Google sign-in gagal

- `503 google_not_configured` → env client platform itu kosong (log startup `Google sign-in not configured` dengan `platform`).
- `400 invalid_redirect_uri` → web: `redirect_uri` dari FE tidak persis sama dengan salah satu `GOOGLE_WEB_REDIRECT_URIS`; desktop: bukan `http://127.0.0.1:<port>/…` / `http://[::1]:<port>/…`.
- `401 google_auth_failed` → log warn `Google sign-in failed` (dengan `platform` dan jawaban Google). `invalid_client` = client ID / secret tidak cocok atau salah platform; `invalid_grant` = code sudah dipakai / kedaluwarsa, atau `code_verifier` / `redirect_uri` berbeda dengan saat otorisasi; `redirect_uri_mismatch` = redirect URI web belum didaftarkan di Google Cloud Console.

### Refresh web (cookie) gagal

- `403 origin_not_allowed` → origin FE belum ada di `CORS_ALLOWED_ORIGINS`, atau FE tidak mengirim `X-Timebox-Client: web`.
- `403` tanpa body → origin ditolak CORS (tidak ada di `CORS_ALLOWED_ORIGINS`).
- `401 session_ended` dengan message `No refresh token` → browser tidak mengirim cookie: API dan aplikasi web beda *site* (`SameSite=Strict`), FE tidak memakai `credentials: 'include'`, atau API diakses lewat `http` di luar `ENV=development` (cookie `Secure`).

### Foto profil `503`

`503 storage_not_configured` → R2 (`R2_ACCOUNT_ID`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_BUCKET`) atau `R2_PUBLIC_URL` belum diisi — log startup `R2 storage or R2_PUBLIC_URL not configured — profile photos disabled`. Foto ter-upload tapi tidak tampil di aplikasi desktop → origin `R2_PUBLIC_URL` belum ada di CSP `img-src`.

### Rate limit / IP whitelist tidak sesuai

Semua client terlihat dari satu IP → `TRUSTED_PROXIES` belum berisi IP/CIDR ingress controller.

## Praktik keamanan

- Jangan commit secret; simpan di Secret Kubernetes.
- Pakai `DB_SSLMODE=require` (atau `verify-ca` / `verify-full`) di production.
- Mengganti `JWT_SECRET` membatalkan semua access token yang sudah terbit dan semua kode email yang belum dipakai (hash-nya memakai kunci turunan `JWT_SECRET`); refresh token tetap berlaku (disimpan di database), jadi client cukup melakukan refresh.
- Isi `TRUSTED_PROXIES` hanya dengan proxy yang memang kamu kelola.
- Setelah SMTP terbukti jalan, set `EMAIL_VERIFICATION_REQUIRED=true` supaya akun hasil signup baru bisa dipakai setelah emailnya terbukti.
- Client secret Google (`GOOGLE_*_CLIENT_SECRET`) dan kunci R2 hanya di Secret; client ID dan `GOOGLE_WEB_REDIRECT_URIS` boleh di ConfigMap.
- API dan aplikasi web harus satu *site* di production (mis. `api.timebox.id` dan `app.timebox.id`) supaya cookie `tb_refresh` (`SameSite=Strict`) terkirim.

## Dokumentasi terkait

- [README utama](../README.md) — setup development dan perintah `make`
- [Kontrak API](../docs/api-contract/README.md)
- [`.env.example`](../.env.example) — semua konfigurasi beserta komentarnya
