# Build stage
# Same Go as go.mod: the official image sets GOTOOLCHAIN=local, so an older
# one refuses to build instead of fetching the right toolchain.
FROM golang:1.26.10-alpine AS builder

WORKDIR /app

# Dependencies first, so source changes reuse the module cache layer
COPY go.mod go.sum ./
RUN go mod download

# golang-migrate, built from source: same version on every architecture,
# verified against the Go checksum database
RUN CGO_ENABLED=0 go install -trimpath -ldflags="-s -w" \
    -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.1

COPY . .

# API server and the initial super admin bootstrap (reads SEED_ADMIN_* env vars)
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o seed-admin ./cmd/seed-admin

# Final stage
FROM alpine:3.22

# UTC everywhere; ENV=production selects the JSON logger and gin release mode
ENV TZ=UTC \
    ENV=production \
    SERVER_PORT=8080

# ca-certificates for HTTPS (Google, R2, SMTP), postgresql-client for the
# entrypoint's wait-for-database, bash for the entrypoint
RUN apk --no-cache add ca-certificates tzdata postgresql-client bash

# Non-root user
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

WORKDIR /app

COPY --from=builder /go/bin/migrate /usr/local/bin/migrate
COPY --from=builder /app/api /app/seed-admin ./
COPY --from=builder /app/internal/database/migrations ./internal/database/migrations
COPY deployments/docker/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN chmod +x /usr/local/bin/docker-entrypoint.sh && chown -R appuser:appuser /app

# Numeric, so Kubernetes can verify runAsNonRoot without a runAsUser
USER 1000:1000

EXPOSE 8080

# Liveness only; point the orchestrator's readiness probe at /ready
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/health || exit 1

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]

# Default command: run the API server
CMD ["/app/api"]
