# Panduan: menambah modul dan resource permission

Prosedur ini jarang dipakai, jadi tidak dimuat ke setiap sesi agent. Konvensi
yang berlaku untuk tiap lapisan dimuat otomatis dari `.claude/rules/` saat file
di folder itu disentuh.

## Membuat modul baru

1. Buat folder `internal/modules/{domain}/{module_name}/`.
2. Buat subfolder `domain/`, `dto/`, `handler/`, `repository/`, `service/`.
3. Buat `main.{module_name}.go` dengan `Initialize(...) *Module` (semua dependency
   sebagai argumen, tanpa setter `Set*`) dan `SetupRoutes(router *gin.RouterGroup)`.
4. Daftarkan modul di [internal/router/router.go](../../internal/router/router.go).
   Repository yang dipakai lintas modul (users, roles) sudah dibangun di sana —
   teruskan, jangan buat ulang.
5. Bila route-nya dicek permission, tambahkan resource permission (di bawah).
6. Tulis kontrak endpoint di `docs/api-contract/{domain}/{module}.md` dan tambahkan
   barisnya di [docs/api-contract/README.md](../api-contract/README.md).
7. Bila modul menyimpan data personal, tambahkan ke `AuthService.ExportAccount` dan
   hapus di `AuthService.CloseAccount` (transaksi yang sama).
8. Catat di `CHANGELOG.md` bagian `## [Unreleased]`.

## Menambah resource permission

1. Tambahkan ke registry di
   [internal/shared/authz/resources.go](../../internal/shared/authz/resources.go)
   (otomatis muncul di `GET /core/v1/roles/permission-catalog`).
2. Di migration modul itu, berikan ke `administrator`:
   ```sql
   UPDATE core.roles SET permissions = permissions || '{"<resource>": "admin"}' WHERE code = 'administrator';
   ```
3. Lindungi route dengan `middleware.RequirePermission("<resource>:<action>")`.
4. Update ERD bila migration mengubah skema (lihat
   [docs/erd/README.md](../erd/README.md)).
