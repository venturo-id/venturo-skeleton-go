# CLAUDE.md

Arsitektur: `docs/architecture.md`; kontrak API: `docs/api-contract/` (dibaca bila perlu).
Aturan per lapisan (handler, repository, service, migration, dokumen API) dimuat dari `.claude/rules/`.

## Perintah

```bash
make build                               # go build ke bin/timebox-api
make test                                # go test -race ./...
make check BASE=origin/<branch-tujuan>   # fmt-check + lint + test + check-changelog + check-doc-sync (sama dengan CI)
make lint BASE=origin/<branch-tujuan>    # golangci-lint, hanya baris yang berubah sejak BASE
make fmt                                 # gofmt + goimports
make hooks                               # sekali per clone: pasang .githooks/commit-msg
make test-integration                    # butuh TEST_DATABASE_URL ke database test yang sudah di-migrate
```

`BASE` default `git config kinerja.base`, lalu `origin/HEAD`; di project turunan set sekali: `git config kinerja.base origin/<branch-tujuan>`. Setup & database: `docs/guides/local-development.md`; modul baru: `docs/guides/new-module.md`.

## Konvensi yang berbeda dari kebiasaan

- Dependency lewat konstruktor (`Initialize(...)`, `NewXService(...)`), tanpa setter `Set*`.
  Pengecualian: backend middleware (`middleware.Set*`) didaftarkan sekali di `internal/router/router.go`.
- Response selalu lewat `internal/shared/response` (envelope `data`, `message`, `meta`,
  `code`, `errors`).
- Otorisasi dari subject live: `middleware.IsSuperAdmin(c)` / `middleware.Caller(c)`.
  `is_super_admin` dan `roles` di JWT hanya informasi dan basi sampai token kedaluwarsa.
- Yang memberi akses (assign role, ubah role, scope API key) atau mengelola user lain
  memeriksa `caller.Covers(perms)`: caller hanya memberi apa yang ia pegang sendiri.
- Mengubah role, permission role, assignment role, status aktif user, atau session epoch
  wajib menginvalidasi authz cache (`InvalidateUser` / `InvalidateAll`) setelah commit —
  tanpa itu permission lama berlaku sampai cache kedaluwarsa.
- Email selalu lewat `mailer.Async` yang diinjeksi; request tidak pernah menunggu SMTP
  (waktunya juga tidak boleh membocorkan apakah akun ada).
- Modul yang menyimpan data personal menambahkannya ke `AuthService.ExportAccount` dan
  menghapusnya di `AuthService.CloseAccount` (transaksi yang sama).
- Semua waktu disimpan dan diolah dalam UTC (`TZ=UTC`, `time.Local` = UTC).

## Gotcha

- `make` meng-`export` isi `.env`: `TEST_DATABASE_URL` / `TEST_MONGO_URI` di `.env` membuat
  `make test` ikut menjalankan test integrasi yang membuat dan menghapus user. Komentar di
  `.env` harus di baris sendiri; `#` ditulis `\#`, `$` ditulis `$$`.
- `config.Load` menolak start bila nilai env tidak terbaca (durasi tanpa satuan `30`
  bukan `30m`, boolean/angka salah, `ENV` atau `DB_SSLMODE` di luar daftar) — bukan jatuh
  ke default diam-diam.
- Redis wajib (authz cache, rate limit) dan `JWT_SECRET` ≥32 karakter wajib di semua `ENV`;
  `EMAIL_VERIFICATION_REQUIRED=true` butuh SMTP. Tanpa itu API tidak start.
- URL database tidak dirangkai manual: Go memakai `DatabaseConfig.GetDSN()` (meng-escape
  tiap bagian), shell memakai `PGPASSWORD="$$DB_PASSWORD"` — `@ : / # ?` di password merusak URL.
- `funlen` dan `gocyclo` melapor di baris `func`, `file-length-limit` di baris terakhir file:
  mengubah isi fungsi panjang bisa lolos `make lint`, tetapi tetap dinilai reviewer.

## Larangan keras

- `err.Error()` dari error tak terduga tidak pernah masuk response (bisa membawa SQL, host):
  pakai `response.InternalError` (log err, kirim pesan umum).
- Token, API key, password, kode OTP, dan alamat email tidak pernah di-log (log warn/error
  diteruskan ke Discord); orang disebut dengan user id.
- Tidak ada kredensial di file yang di-commit (termasuk migration dan seed); super admin dari
  `SEED_ADMIN_*` lewat `make seed-admin`.
- `make db-reset` dan `make test-integration` hanya ke database yang datanya boleh hilang.
- `//nolint` wajib menyebut linter dan alasannya (`//nolint:mnd // port default IANA`).
- Tidak push ke branch PR setelah PR di-approve (dihitung pelanggaran, tidak direview).

## Standar review Kinerja

PR dinilai otomatis di kinerja.venturo.id; `make check` dan hook menegakkan sebagian besar.

- Struktur (−10 per temuan): fungsi ≤50 baris dari `func` sampai `}` termasuk baris kosong
  dan komentar; kompleksitas ≤10; nesting ≤4 dari badan fungsi; parameter ≤5; file ≤800
  baris; tanpa duplikasi (`dupl`); tanpa magic value di logika (`mnd`, `goconst`; literal di
  test, config, dan log dikecualikan); tanpa kode mati (`unused`).
- Keterbacaan: camelCase/PascalCase dengan initialism Go (`ID`, `APIKey`, `URL`), boolean
  `is/has/should/can`, parameter dinamai sesuai isinya (`userID`, bukan `s`/`id2`);
  tanggal-waktu di API RFC3339 UTC.
- Doc sync (10 poin, biner): perubahan endpoint, DTO, route, atau skema membawa dokumennya
  di PR yang sama (`.claude/rules/api-docs.md`, dicek `make check-doc-sync`). Setiap
  perubahan kode Go non-test = entri di `CHANGELOG.md` bagian `## [Unreleased]`.
- Setiap fungsi yang diubah punya test yang menutupinya.
- Commit: subject `type(scope)?!?: deskripsi` (type `build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test`)
  dan kode issue Space `[KODE-123]` di setiap commit; tanpa itu PR tidak dinilai.
- Deskripsi PR ditulis sebelum push pertama (mengedit deskripsi tidak memicu review ulang)
  dan hanya berisi fakta yang ada di diff.

## Delegasi sub-agent

Batas umum: sub-agent tidak commit, push, atau membuat PR; hasilnya diperiksa agent utama.

1. **Eksplorasi sebelum mengubah kode** — agent `Explore`
   - Pemicu: akan mengubah fungsi, DTO, atau query yang dipakai di luar file itu.
   - Input: path file + nama simbol yang akan diubah; scope `internal/`, `pkg/`, `docs/api-contract/`.
   - Output: daftar `path:line` caller, test yang menutupinya, dan dokumen kontrak terkait — satu baris per item, maks 30 baris.
   - Batas: read-only; maks 4 paralel (satu per file atau modul).
2. **Rencana perubahan besar** — agent `Plan`
   - Pemicu: perubahan menyentuh lebih dari 3 file, lebih dari satu modul, atau alur sesi/auth/authz.
   - Input: tujuan issue Space, hasil eksplorasi, file `.claude/rules/` yang berlaku.
   - Output: langkah bernomor; tiap langkah menyebut file, test yang ditambah, dan dokumen yang diupdate.
   - Batas: read-only; 1 agent.
3. **Verifikasi calon temuan** — agent `general-purpose`
   - Pemicu: ada temuan dari review, lint, atau agent lain yang belum terbukti.
   - Input: satu temuan (kategori, `path:line`, klaim); scope file itu dan caller-nya.
   - Output: `CONFIRMED` / `REJECTED` + bukti `path:line` + alasan maks 2 kalimat.
   - Batas: read-only (boleh menjalankan `go test` dan `make lint`); maks 3 paralel.
4. **Review diff sebelum PR** — agent `general-purpose`
   - Pemicu: sebelum push pertama atau sebelum membuka PR.
   - Input: output `git diff <BASE>...HEAD`, bagian "Standar review Kinerja" di atas, dan rules yang berlaku untuk file di diff.
   - Output: daftar `[severity] kategori path:line — ringkasan` maks 15 butir, atau `bersih`.
   - Batas: read-only; 1 agent.
5. **Sinkron dokumen setelah API atau skema berubah** — agent `general-purpose`
   - Pemicu: `make check-doc-sync` atau `check-changelog` gagal.
   - Input: diff kode API/migration; scope hanya `docs/api-contract/`, `docs/erd/`, `CHANGELOG.md`.
   - Output: daftar file dokumen yang diubah + satu kalimat per file.
   - Batas: boleh edit hanya file di scope itu; 1 agent.
