---
paths:
  - "internal/modules/**/repository/**"
  - "internal/database/*.go"
---

# Repository

- Repository memegang `*pgxpool.Pool` dan menjalankan setiap statement lewat
  `r.q(ctx)` (= `database.Querier(ctx, r.db)`), supaya ikut transaksi `WithTx` yang
  dibawa ctx. Jangan memanggil `r.db.Query/Exec` langsung.
- Satu konstanta daftar kolom + satu `scanX(pgx.Row)` per entity.
- UPDATE/DELETE yang tidak mengenai baris mengembalikan `database.ErrNotFound`.
- Keunikan dijaga constraint database: periksa `database.UniqueViolation(err)` setelah
  write, atau terjemahkan ke sentinel di repo (`ErrEmailTaken`, `ErrUsernameTaken`) —
  query "sudah ada?" sebelum insert punya race di antaranya.
- Data milik satu user (mis. API key) difilter `user_id` di query repository, bukan
  di service.
- List: `ORDER BY created_at DESC, id DESC` (urutan stabil untuk paginasi).
- Sediakan `FindByIDForUpdate` (`SELECT … FOR UPDATE`) untuk entity yang diubah bersamaan,
  dan update parsial lewat struct perubahan (`RoleRepository.Update` dengan `RoleChanges`).
- Query selalu berparameter (`$1`, `$2`); jangan merangkai input ke SQL.
