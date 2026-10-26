# Changelog

Semua perubahan penting di repo ini dicatat di sini. Formatnya mengikuti
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Setiap PR yang mengubah
kode Go non-test menambah entri di `## [Unreleased]` (dicek `make check-changelog`).

## [Unreleased]

### Added

- `.golangci.yml` (golangci-lint v2) dengan ambang review Kinerja: `funlen` 50 baris,
  `gocyclo` 10, `revive` (`argument-limit` 5, `max-control-nesting` 4,
  `file-length-limit` 800, `var-naming`, `exported`), `dupl`, `mnd`, `goconst`, `unused`,
  `errcheck`, `govet` + `nilness`, `staticcheck`, formatter `gofmt` + `goimports`.
- `make check` (fmt-check + lint + `test -race` + check-changelog + check-doc-sync),
  `make fmt-check`, `make lint-all`, dan `make hooks`. `BASE` diambil dari
  `git config kinerja.base`, lalu `origin/HEAD`.
- `scripts/check-changelog.sh`: gagal bila kode Go non-test berubah tanpa entri di
  `CHANGELOG.md`.
- `scripts/check-doc-sync.sh`: gagal bila handler, DTO, router, atau migration berubah
  tanpa update `docs/api-contract/` atau `docs/erd/`, atau bila kontrak berubah tanpa
  penanda `**Last Updated:**` baru.
- `.githooks/commit-msg`: format Conventional Commit dan kode issue Space `[KODE-123]`,
  sama dengan gate review Kinerja.
- CI GitHub Actions (`.github/workflows/ci.yml`): `make check` dan batas 200 baris
  `CLAUDE.md` di setiap pull request.
- Template pull request, `.claude/rules/` per lapisan, `.claude/settings.json` dengan
  marketplace plugin `venturo-tools` serta plugin `venturo-kinerja` dan mod
  `venturo-kinerja-hud` yang langsung aktif (ditawarkan saat repo dibuka).
- `docs/architecture.md`, `docs/guides/local-development.md`, `docs/guides/new-module.md`.

### Changed

- `make lint` menjalankan golangci-lint (v2.15.0, lewat `go run`) dan hanya melaporkan
  baris yang berubah sejak `BASE`; menggantikan gofmt + `go vet` + staticcheck.
  Seluruh repo: `make lint-all`.
- `make fmt` memakai `golangci-lint fmt` (gofmt + goimports).
- `CLAUDE.md` ditulis ulang (302 → 114 baris): perintah, konvensi, gotcha, larangan,
  standar review Kinerja, dan delegasi sub-agent. Isi arsitektur dan prosedur
  dipindah ke `docs/`, aturan per folder ke `.claude/rules/`.
