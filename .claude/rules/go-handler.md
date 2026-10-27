---
paths:
  - "internal/modules/**/handler/**"
  - "internal/modules/**/dto/**"
---

# Handler dan DTO

- Input lewat `request.BindJSON` / `request.BindQuery` / `request.BindOptionalJSON` /
  `request.UUIDParam(c, "id")` (`internal/shared/request`): helper itu sudah menulis
  400-nya dan mengembalikan `false` — handler cukup `return`, jangan menulis respons
  kedua.
- Caller: `middleware.MustGetUserID(c)`; untuk grant ceiling `middleware.Caller(c)`.
- Satu `handleError(c, err, fallback)` per handler: switch `errors.Is` / `errors.As`
  (atau tabel `knownErrors` seperti `auth/handler/errors.go`), default
  `response.InternalError(c, fallback, err)`. Jangan memetakan error di tiap method.
- Error ke client membawa `code` stabil (`response.ErrorCode` / `response.Fail`);
  kode baru didaftarkan di `docs/api-contract/README.md#error-codes`. `err.Error()`
  hanya boleh dikirim bila pesannya dibangun service dari request saja, dan ditandai
  komentar (contoh: `ErrInvalidPermissions` di `role.handler.go`).
- List: service mengembalikan `([]dto.XResponse, total int64, error)`; handler
  memanggil `response.SuccessWithPagination(c, http.StatusOK, msg, items, params.Page,
  params.Limit, total)`. Query `page` (default 1, maks 100000), `limit` (default 10,
  maks 100).
- Validasi lewat tag `binding` (validator kustom `username`, `password`, `timezone` di
  `pkg/validator`). Email dan username dinormalisasi dengan
  `validator.NormalizeEmail` / `validator.NormalizeUsername` sebelum dibandingkan
  atau disimpan — CHECK constraint menolak yang belum lowercase/trim.
- Waktu di DTO: `time.Time` (JSON RFC3339, UTC karena `TZ=UTC`) atau `types.Date`
  (`2006-01-02`); jangan string dengan format lain.
- Rantai route: `JWTAuth()` → `RequirePermission("<resource>:<action>")`. Endpoint
  yang mengelola kredensial, sesi, atau akun caller sendiri menambah
  `middleware.DenyAPIKeyAuth()` setelah `JWTAuth()`. Setiap endpoint list/read
  non-publik punya `RequirePermission` sendiri — tidak ada scope tenant yang
  menyembunyikan data.
