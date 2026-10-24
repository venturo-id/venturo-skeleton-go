#!/bin/bash
set -e

# Runs the database migrations, then the container's command.
#
#   SKIP_MIGRATION=true   start without migrating — set it on the API
#                         deployment and run the migrations in a one-off
#                         Job instead, so a failed or long migration does
#                         not crash-loop every API pod.
#
# The initial super admin is created once, by hand:
#   kubectl exec <pod> -- /app/seed-admin   (with SEED_ADMIN_* set)

# Seconds to wait for another replica's migration lock (not an env var)
MIGRATE_LOCK_TIMEOUT=300

wait_for_db() {
    echo "Waiting for database ${DB_HOST:-localhost}:${DB_PORT:-5432}/${DB_NAME} ..."

    # Maximum wait time in seconds (5 minutes)
    MAX_WAIT=300
    ELAPSED=0

    until PGPASSWORD="$DB_PASSWORD" psql -h "${DB_HOST:-localhost}" -p "${DB_PORT:-5432}" -U "$DB_USER" -d "$DB_NAME" -c '\q' 2>/dev/null; do
        if [ $ELAPSED -ge $MAX_WAIT ]; then
            echo "ERROR: Database is not ready after ${MAX_WAIT} seconds. Exiting..."
            exit 1
        fi

        echo "Database is unavailable - sleeping (${ELAPSED}s/${MAX_WAIT}s)"
        sleep 2
        ELAPSED=$((ELAPSED + 2))
    done

    echo "Database is ready!"
}

run_migrations() {
    echo "Running core migrations..."

    # The password stays out of the URL — an @ : / # ? in it would break the
    # URL — and reaches migrate via PGPASSWORD. migrate takes a Postgres
    # advisory lock; a replica starting while another migrates waits for it
    # up to MIGRATE_LOCK_TIMEOUT seconds (migrate's own default is 15s, after
    # which the pod would exit and crash-loop), then finds nothing to do.
    DB_URL="postgresql://${DB_USER}@${DB_HOST:-localhost}:${DB_PORT:-5432}/${DB_NAME}?sslmode=${DB_SSLMODE:-disable}"

    PGPASSWORD="$DB_PASSWORD" migrate -path /app/internal/database/migrations/core \
        -database "${DB_URL}&x-migrations-table=schema_migrations_core" \
        -lock-timeout "$MIGRATE_LOCK_TIMEOUT" \
        up || {
            echo "ERROR: Core migrations failed"
            exit 1
        }

    echo "Core migrations completed successfully!"
}

echo "==================================="
echo "Timebox Backend - Starting up"
echo "==================================="

if [ "${SKIP_MIGRATION}" = "true" ]; then
    echo "SKIP_MIGRATION is set to true. Skipping migrations..."
else
    wait_for_db
    run_migrations
fi

echo "Starting application..."

# Execute the main command (passed as arguments to this script)
exec "$@"
