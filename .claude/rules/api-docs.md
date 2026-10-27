---
paths:
  - "docs/api-contract/**"
  - "docs/erd/**"
---

# Dokumen kontrak API dan ERD

- Satu file per modul: `docs/api-contract/<domain>/<module>.md`, terdaftar di tabel
  "Documents" di `docs/api-contract/README.md` (jumlah endpoint ikut diupdate).
- Kepala file: `**Version:**`, `**Base URL:**`, `**Module:**`, `**Last Updated:** YYYY-MM-DD`.
- Penanda last-updated ada dua dan keduanya diupdate di setiap perubahan isi:
  - baris kepala `**Last Updated:** YYYY-MM-DD` (tanggal saja);
  - baris penutup di akhir file `**Last Updated:** YYYY-MM-DD (<ringkasan perubahan>)`.
  `make check-doc-sync` menolak file kontrak yang berubah tanpa baris
  `**Last Updated:**` baru.
- Perubahan yang mengubah perilaku client ditulis sebagai bagian
  `## ⚠️ Breaking Change (YYYY-MM-DD) — <judul>` atau
  `## ⚠️ Behaviour Change (YYYY-MM-DD) — <judul>` di atas `## Overview`, terbaru di
  atas, berisi poin yang menyebut endpoint, field, dan kode error yang berubah.
- Contoh request/response memakai envelope `internal/shared/response` (`data`,
  `message`, `meta.pagination`, `code`, `errors`, `retry_after`), timestamp RFC3339
  UTC, dan data contoh fiktif (bukan email/nama orang sungguhan).
- Kode error baru ditambahkan ke daftar `docs/api-contract/README.md#error-codes`.
- ERD: edit `docs/erd/<module>.mmd`, render ulang `.svg`/`.png` sesuai
  `docs/erd/README.md`, dan perbarui rentang migration di judulnya.
