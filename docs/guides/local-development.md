# Panduan: development lokal

Dipindah dari `CLAUDE.md` lama. Daftar lengkap perintah: `make help` atau
[README](../../README.md).

## Setup

```bash
cp .env.example .env   # JWT_SECRET dan SEED_ADMIN_* wajib; DB_*/REDIS_* harus menjangkau server yang hidup
make db-setup          # migrate-up + super admin dari SEED_ADMIN_*
make hooks             # pasang commit-msg hook (format commit Kinerja)
make dev               # hot reload (air, konfigurasi di .air.toml; error build di build-errors.log)
```

Tidak ada seeder SQL: role sistem `super_admin` dan `administrator` dibuat oleh
migration `000008`.

## Database

```bash
make migrate-up                                  # semua migration yang belum jalan
make migrate-down MODULE=core                    # rollback satu migration terakhir
make migrate-version                             # versi saat ini
make migrate-create NAME=create_foo MODULE=core  # file migration baru
make migrate-force V=1 MODULE=core               # paksa versi (bila dirty)
make db-reset                                    # drop + db-setup (ditolak bila ENV=production)
```

Migration core ada di `internal/database/migrations/core`, dicatat di
`schema_migrations_core`. Migration yang gagal di-rollback tapi versinya dirty:
perbaiki penyebabnya, `make migrate-force V=<versi sebelumnya> MODULE=core`, lalu
`make migrate-up`.

## Test

```bash
make test                                 # go test -race ./...
make test-integration                     # TestIntegration_* ke TEST_DATABASE_URL (+ TEST_MONGO_URI opsional)
go test ./internal/modules/core/auth/...  # satu paket
go test -cover ./...                      # dengan coverage
```

## Mencoba endpoint

```bash
curl http://localhost:8080/health
curl http://localhost:8080/ready

curl -X POST http://localhost:8080/core/v1/auth/signin \
  -H "Content-Type: application/json" \
  -d '{"login": "<SEED_ADMIN_EMAIL>", "password": "<SEED_ADMIN_PASSWORD>"}'

curl http://localhost:8080/core/v1/users -H "Authorization: Bearer <ACCESS_TOKEN>"
```

## Email lokal

Tanpa SMTP setiap email hanya di-log di level debug, termasuk kodenya — cukup untuk
development, diam di production. Konfigurasi SMTP lengkap ada di
[.env.example](../../.env.example).
