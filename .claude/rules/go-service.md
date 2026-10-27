---
paths:
  - "internal/modules/**/service/**"
---

# Service

- Transaksi: `database.WithTx(ctx, pool, func(ctx context.Context) error { ... })`.
  Panggilan repository dengan ctx itu ikut transaksi, lintas modul; `WithTx` di
  dalamnya bergabung ke yang luar.
- Update tidak menulis balik baris yang dibaca di luar transaksinya: baca dengan row lock
  di dalam `WithTx` (`FindByIDForUpdate`, seperti `UserService.Update`) atau tulis hanya
  kolom yang berubah. Tulis ulang seluruh baris membatalkan deaktivasi, ganti email, atau
  penyempitan permission yang terjadi bersamaan.
- Error repository (`database.ErrNotFound`, unique violation / `ErrEmailTaken`)
  diterjemahkan ke error domain di service; tidak ada query "sudah ada?" sebelum insert.
- Kerja lambat (bcrypt, HTTP, upload R2) di luar transaksi: sesi database punya
  `idle_in_transaction_session_timeout` 60 detik.
- Mengecek password selalu lewat `reservePasswordAttempt` (hitungan dicatat sebelum
  compare, satu `UPDATE` atomik) lalu `ClearPasswordAttempts` yang terikat ke hash
  yang dibandingkan. Jangan memutuskan lock dari baris user yang dibaca sebelumnya.
- Apa pun yang menerbitkan token atau mengakhiri sesi mengunci baris user
  (`UserRepository.FindByIDForUpdate`) di transaksi yang sama; mengakhiri semua sesi
  lewat `AuthService.RevokeAllSessions` atau `revokeSessionsTx` di dalam transaksi
  yang mengubah kredensial, lalu invalidasi subject setelah commit.
- Perubahan yang bisa menyerahkan akun atau datanya ke orang lain (seperti ganti
  email, hapus akun) memanggil `reauthenticate` dulu, lalu transaksi perubahannya
  dimulai dengan `confirmReauthTx`.
- `is_email_verified` hanya di-set bila kepemilikan email terbukti (dibuat/dijamin
  admin, seed, Google, kode verifikasi, reset password) — tidak pernah untuk self
  signup atau ganti email oleh user sendiri.
- Waktu yang bergantung pada user (batas hari, "hari ini") memakai
  `time.LoadLocation(user.Timezone)`; penyimpanan tetap UTC.
- Audit: perubahan keamanan dan administrasi mencatat `audit.Entry` lewat
  `audit.Recorder` yang diinjeksi. `Details` berisi fakta dan nama field — tidak
  pernah data personal, secret, atau login yang diketik; orang disebut dengan user id.
