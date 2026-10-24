# ERD — skema `core`

Diturunkan dari [internal/database/migrations/core](../../internal/database/migrations/core)
(migration `000001`–`000011`): 7 tabel, 1 enum. Audit log tidak ada di sini — disimpan di MongoDB (koleksi `audit_logs`, lihat [internal/shared/audit](../../internal/shared/audit/audit.go)).

![ERD skema core](core.svg)

| File | Fungsi |
|---|---|
| [core.mmd](core.mmd) | Sumber Mermaid — **edit file ini**, lalu render ulang |
| [core.svg](core.svg) | Hasil render vektor (tajam saat di-zoom) |
| [core.png](core.png) | Hasil render raster, langsung terbuka di VS Code |

## Cara membaca

| Simbol | Arti |
|---|---|
| Garis solid | `FOREIGN KEY` constraint di database |
| Garis putus-putus | Referensi logis **tanpa FK**, divalidasi di service layer |
| `\|\|` / `\|o` | Tepat satu / nol atau satu (kolom FK nullable) |
| `o{` | Nol atau banyak |
| `audit standard_columns` | `created_at`, `created_by`, `updated_at`, `updated_by`, `deleted_at`, `deleted_by` — soft delete |

Kolom `created_by` / `updated_by` / `deleted_by` adalah UUID user tanpa FK dan tidak
digambar sebagai relasi.

Warna menandai domain:

| Warna | Domain | Tabel |
|---|---|---|
| Biru | Identity & auth | `users`, `refresh_tokens`, `account_tokens`, `user_identities`, `api_keys` |
| Oranye | RBAC | `roles`, `user_roles` |

## Model akses

Aplikasi ini single-tenant: tidak ada client, company, atau branch. Akses ditentukan
sepenuhnya oleh role global.

```
users ── user_roles ── roles (permissions JSONB)
  ├─ api_keys        (milik satu user, scope ∩ permission user saat request)
  ├─ refresh_tokens  (satu sesi = rantai token rotasi dengan session_id yang sama)
  ├─ account_tokens  (kode OTP verifikasi email, reset password, ganti email; reset grant)
  └─ user_identities (Google, dst.)
```

User hasil signup (email maupun Google) tidak mendapat role apa pun sampai
ada yang memberikannya, dan role hanya bisa diberikan ke user yang emailnya
sudah terverifikasi.

## Catatan desain

- **Soft delete + partial unique index.** Unique pada `email`, `username`, `roles.code`,
  `key_id`, dll. hanya berlaku `WHERE deleted_at IS NULL`, sehingga nilai bisa dipakai
  ulang setelah soft delete.
- **Email & username selalu lowercase.** CHECK constraint `users_email_normalized` /
  `users_username_normalized` menjamin nilainya sudah di-lowercase dan di-trim, sehingga
  unique index-nya otomatis case-insensitive. Aplikasi menormalisasi di setiap input.
- **Rantai rotasi refresh token.** `refresh_tokens.rotated_from_id` menunjuk token yang
  digantikan saat `/auth/refresh`. Token yang sudah dirotasi lalu dipakai lagi (lewat
  30 detik) dianggap bocor → semua sesi user di-revoke.
- **Satu sumber role.** `user_roles` adalah satu-satunya tempat penugasan role. Permission
  efektif user = gabungan permission semua role aktifnya, di-cache di Redis per user
  (lihat [authz/service.go](../../internal/shared/authz/service.go)).
- **Epoch sesi.** `users.tokens_valid_after` menolak semua access token yang terbit
  sebelumnya; maju saat logout-all, ganti atau reset password, akun diklaim pemilik
  email, user dinonaktifkan atau menutup akunnya, atau refresh token bocor terdeteksi.
- **Sesi.** `refresh_tokens.session_id` dan `signed_in_at` tetap sepanjang rantai
  rotasi; access token membawa `session_id` sebagai claim `sid`. Logout atau mengakhiri
  satu sesi me-revoke refresh token-nya, dan access token sesi itu ikut ditolak (lewat
  daftar sesi yang baru berakhir di cache authz). Rotasi (revoke + token baru) satu
  transaksi, jadi sesi yang masih hidup tidak pernah terlihat berakhir.
- **Zona waktu.** `users.timezone` (IANA, default `Asia/Jakarta`) hanya menentukan cara
  tanggal ditampilkan dan dikelompokkan untuk user itu; semua timestamp tetap UTC.
- **Tutup akun.** Baris `users` tidak dihapus (tabel lain merujuknya) tapi dianonimkan:
  email dan username jadi placeholder dari id, nama/telepon/avatar/password dikosongkan.
  `user_identities`, `refresh_tokens`, `account_tokens`, `user_roles`, dan `api_keys`
  milik user dihapus.
- **Kode email (OTP).** `account_tokens` menyimpan kode 6 digit sekali pakai untuk
  verifikasi email, reset password, dan ganti email (sejak `000012`, menggantikan link).
  Yang disimpan hanya HMAC-SHA256 dengan kunci server atas id baris + kode — enam digit
  terlalu sedikit untuk hash biasa. Kode dicari per (user, purpose), bukan per hash: baris
  terbaru yang belum dipakai dikunci (`FOR UPDATE`), jadi tebakan diperiksa satu per satu;
  `attempts` menghitung tebakan salah dan kode mati di `OTP_MAX_ATTEMPTS`. Kode terikat
  ke alamat akun saat dibuat (`email`): batal begitu email user berbeda. Kode ganti email
  dikirim ke `new_email`. Kode reset password yang benar ditukar dengan `reset_grant`:
  token acak (hash SHA-256) berumur pendek yang dipakai lewat satu `UPDATE … RETURNING`.
  Menerbitkan kode/grant baru me-retire yang lama untuk tujuan yang sama (`used_at`
  diisi). Yang kedaluwarsa dihapus tiap jam bersama refresh token.
- **Identitas Google.** `user_identities.provider_user_id` untuk Google adalah `sub` akun
  Google (sejak `000012`; identitas lama dari Firebase dipindah ke `sub`-nya). Satu akun
  me-link paling banyak satu akun Google (dijaga aplikasi).
- **Role sistem dari migration.** `super_admin` dan `administrator` dibuat oleh migration
  `000008`, bukan seeder. Resource baru di-grant ke `administrator` lewat migration
  modulnya.

## Enum

| Nama | Isi |
|---|---|
| `core.api_key_environment` | `live`, `test` |

## Render ulang

Setelah menambah migration, perbarui `core.mmd`, lalu jalankan dari root repo:

```bash
npx -y @mermaid-js/mermaid-cli@11.17.0 -i docs/erd/core.mmd -o docs/erd/core.svg -b white -w 2400
npx -y @mermaid-js/mermaid-cli@11.17.0 -i docs/erd/core.mmd -o docs/erd/core.png -b white -w 2400 -s 2
```

Perintah pertama mengunduh Chromium untuk Puppeteer. Kalau Google Chrome sudah
terpasang, set `PUPPETEER_SKIP_DOWNLOAD=1` dan arahkan ke Chrome lewat
`-p puppeteer.json` berisi `{"executablePath": "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}`.
Di Linux/WSL: `{"executablePath": "/usr/bin/google-chrome", "args": ["--no-sandbox"]}`.
