---
paths:
  - "internal/database/migrations/**"
---

# Migration

- Buat dengan `make migrate-create NAME=<nama> MODULE=core`; setiap `up` punya `down`
  yang benar-benar membalikkannya.
- Expand/contract: migration harus tetap jalan dengan rilis sebelumnya (rolling
  update dan rollback menjalankannya bersama skema baru). Kolom `NOT NULL` baru
  diberi `DEFAULT`; kolom yang masih dibaca rilis sebelumnya tidak di-rename atau
  di-drop di rilis yang sama (contoh: `000011`).
- `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`; timestamp
  `TIMESTAMP WITH TIME ZONE` (UTC); index di setiap foreign key; flag, counter, dan
  timestamp audit yang dibaca ke field non-pointer: `NOT NULL` dengan default.
- Resource permission baru diberikan ke `administrator` di migration modulnya:
  `UPDATE core.roles SET permissions = permissions || '{"<resource>": "admin"}' WHERE code = 'administrator';`
- Migration yang mengubah skema = update `docs/erd/<module>.mmd` dan render ulang
  (`docs/erd/README.md`) di PR yang sama; `make check-doc-sync` menolak tanpa itu.
- Migration gagal meninggalkan versi dirty: perbaiki penyebabnya,
  `make migrate-force V=<versi sebelumnya> MODULE=core`, lalu `make migrate-up`.
