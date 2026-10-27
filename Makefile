.PHONY: help migrate-up migrate-down migrate-create migrate-force migrate-version seed seed-admin db-setup db-reset dev build run test test-integration lint lint-all fmt fmt-check check-changelog check-doc-sync check hooks

help:
	@echo "Available commands:"
	@echo ""
	@echo "Development:"
	@echo "  make dev                     - Run with hot reload (Air)"
	@echo "  make build                   - Build application binary"
	@echo "  make run                     - Run application"
	@echo "  make test                    - Run all tests with the race detector"
	@echo "  make test-integration        - Run the database tests (needs TEST_DATABASE_URL; TEST_MONGO_URI optional)"
	@echo "  make lint                    - golangci-lint on the lines changed since BASE"
	@echo "  make lint-all                - golangci-lint on the whole codebase (existing backlog included)"
	@echo "  make fmt                     - Format all Go files (gofmt + goimports)"
	@echo "  make fmt-check               - Fail when a Go file is not formatted"
	@echo "  make check                   - fmt-check + lint + test + check-changelog + check-doc-sync (what CI runs)"
	@echo "  make hooks                   - Use .githooks (commit message format of the Kinerja review)"
	@echo ""
	@echo "Database migrations:"
	@echo "  make migrate-up              - Run all pending migrations"
	@echo "  make migrate-down MODULE=core - Rollback last migration of a module"
	@echo "  make migrate-create NAME=xxx MODULE=core - Create new migration file"
	@echo "  make migrate-force V=xxx MODULE=core     - Force migration to specific version"
	@echo "  make migrate-version         - Show current migration version"
	@echo ""
	@echo "Seeding commands:"
	@echo "  make seed                    - Create the initial super admin (same as seed-admin)"
	@echo "  make seed-admin              - Create the initial super admin from SEED_ADMIN_* env vars"
	@echo ""
	@echo "Setup commands:"
	@echo "  make db-setup                - Run migrations + create the super admin (fresh setup)"
	@echo "  make db-reset                - Drop, migrate, and seed database (refused when ENV=production)"

# Database migrations
# The password stays out of the URL — an @ : / # ? in it would break the URL.
# migrate and psql read it from PGPASSWORD, set from the exported DB_PASSWORD
# with shell quoting ("$$DB_PASSWORD") so any character survives.
DB_URL=postgresql://$(DB_USER)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=$(DB_SSLMODE)
MIGRATIONS_PATH=internal/database/migrations

# golangci-lint is run through `go run` at a pinned version, so it is always
# built with this module's Go version. Settings in .golangci.yml.
GOLANGCI_LINT=github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

# The branch a change is compared with by lint, check-changelog and
# check-doc-sync: the remote's default branch unless given, e.g.
# `make check BASE=origin/production-simple`. CI passes the PR's base.
BASE ?= $(shell git config --get kinerja.base 2>/dev/null || git rev-parse --abbrev-ref origin/HEAD 2>/dev/null)

# Values from .env, when present (a fresh clone or CI has none)
-include .env
export

# Unset or empty, the database settings fall back to the defaults of
# internal/config, so migrate-* and db-reset reach the database the API
# would. An empty sslmode would mean require to migrate's driver (lib/pq).
DB_HOST := $(or $(DB_HOST),localhost)
DB_PORT := $(or $(DB_PORT),5432)
DB_USER := $(or $(DB_USER),postgres)
DB_PASSWORD := $(or $(DB_PASSWORD),postgres)
DB_NAME := $(or $(DB_NAME),timebox)
DB_SSLMODE := $(or $(DB_SSLMODE),disable)

migrate-up:
	@echo "Running migrations..."
	@if [ -d "$(MIGRATIONS_PATH)/core" ]; then \
		echo "Running core migrations..."; \
		PGPASSWORD="$$DB_PASSWORD" migrate -path $(MIGRATIONS_PATH)/core -database "$(DB_URL)&x-migrations-table=schema_migrations_core" up; \
	fi

migrate-down:
	@if [ -z "$(MODULE)" ]; then echo "Error: MODULE is required. Usage: make migrate-down MODULE=core"; exit 1; fi
	@echo "Rolling back last migration for module $(MODULE)..."
	PGPASSWORD="$$DB_PASSWORD" migrate -path $(MIGRATIONS_PATH)/$(MODULE) -database "$(DB_URL)&x-migrations-table=schema_migrations_$(MODULE)" down 1

migrate-create:
	@if [ -z "$(NAME)" ]; then echo "Error: NAME is required. Usage: make migrate-create NAME=your_migration_name MODULE=core"; exit 1; fi
	@if [ -z "$(MODULE)" ]; then echo "Error: MODULE is required. Available: core"; exit 1; fi
	@echo "Creating migration: $(NAME) in module $(MODULE)"
	migrate create -ext sql -dir $(MIGRATIONS_PATH)/$(MODULE) -seq $(NAME)

migrate-force:
	@if [ -z "$(V)" ]; then echo "Error: V is required. Usage: make migrate-force V=1 MODULE=core"; exit 1; fi
	@if [ -z "$(MODULE)" ]; then echo "Error: MODULE is required. Available: core"; exit 1; fi
	PGPASSWORD="$$DB_PASSWORD" migrate -path $(MIGRATIONS_PATH)/$(MODULE) -database "$(DB_URL)&x-migrations-table=schema_migrations_$(MODULE)" force $(V)

migrate-version:
	@echo "Core module version:"
	@PGPASSWORD="$$DB_PASSWORD" migrate -path $(MIGRATIONS_PATH)/core -database "$(DB_URL)&x-migrations-table=schema_migrations_core" version

# Initial super admin — credentials come from SEED_ADMIN_* in .env, never from
# a committed file. System roles are created by the migrations.
seed-admin:
	@echo "Creating initial super admin..."
	@go run ./cmd/seed-admin

seed: seed-admin

# Database setup commands
db-setup:
	@echo "🚀 Setting up database..."
	@$(MAKE) migrate-up
	@echo ""
	@$(MAKE) seed
	@echo "✅ Database setup completed!"

db-reset:
	@if [ "$(ENV)" = "production" ]; then echo "Error: db-reset is refused when ENV=production"; exit 1; fi
	@echo "⚠️  Resetting database $(DB_NAME) on $(DB_HOST) (this will drop all data)..."
	@PGPASSWORD="$$DB_PASSWORD" psql -h $(DB_HOST) -p $(DB_PORT) -U $(DB_USER) -d postgres -c "DROP DATABASE IF EXISTS $(DB_NAME) WITH (FORCE);"
	@PGPASSWORD="$$DB_PASSWORD" psql -h $(DB_HOST) -p $(DB_PORT) -U $(DB_USER) -d postgres -c "CREATE DATABASE $(DB_NAME);"
	@echo "Database dropped and recreated!"
	@$(MAKE) db-setup

# Development commands
build:
	@echo "🔨 Building application..."
	@go build -o bin/timebox-api ./cmd/api
	@echo "✅ Build completed: bin/timebox-api"

run:
	@echo "🚀 Running application..."
	@go run ./cmd/api

dev:
	@echo "🔥 Starting development server with hot reload..."
	@~/go/bin/air

test:
	@go test -race ./...

# Integration tests run against a real, migrated database — never one whose
# data matters: they create and delete users. TEST_MONGO_URI (optional)
# adds the audit log tests, which use and drop a database of their own.
test-integration:
	@if [ -z "$(TEST_DATABASE_URL)" ]; then echo "Error: set TEST_DATABASE_URL to a migrated test database, e.g. postgres://postgres:secret@localhost:5432/timebox_test?sslmode=disable"; exit 1; fi
	@go test -race -count=1 -run Integration ./...

fmt:
	@go run $(GOLANGCI_LINT) fmt ./...

fmt-check:
	@out=$$(go run $(GOLANGCI_LINT) fmt --diff ./...) || { echo "Not formatted (run make fmt):"; echo "$$out"; exit 1; }

# Only issues on lines changed since BASE fail: the code from before
# .golangci.yml has a backlog (make lint-all) that is paid down separately.
lint:
	@if [ -z "$(BASE)" ]; then echo "Error: BASE is empty; set it once with git config kinerja.base <base-branch>, or pass make lint BASE=<base-branch>"; exit 1; fi
	@go run $(GOLANGCI_LINT) run --new-from-merge-base=$(BASE) ./...
	@echo "✅ Lint passed"

lint-all:
	@go run $(GOLANGCI_LINT) run ./...

check-changelog:
	@scripts/check-changelog.sh $(BASE)

check-doc-sync:
	@scripts/check-doc-sync.sh $(BASE)

check: fmt-check lint test check-changelog check-doc-sync
	@echo "✅ All checks passed"

hooks:
	@git config core.hooksPath .githooks
	@echo "✅ Git hooks enabled (.githooks)"
