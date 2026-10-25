#!/usr/bin/env bash
# Wipes every SL-Bus table and the migration history, so `migrate up` can
# rebuild the database from 000001. ALL DATA IS LOST.
#
# Only drops what our migrations create (the nine app schemas and
# public.schema_migrations). Other schemas, such as a hosting provider's
# auth/storage schemas, and the slbus_app role are left alone.
#
# Usage:
#   scripts/reset-db.sh "<database-url>"            # wipe only
#   scripts/reset-db.sh "<database-url>" --migrate  # wipe, then migrate up
#
# The URL is a required argument on purpose (never read from .env), and the
# script asks you to type the database host before it does anything.
# Use the migration/superuser URL (MIGRATE_DATABASE_URL), not the app one.
#
# Requires: psql. With --migrate also requires the `migrate` CLI.

set -euo pipefail

DB_URL="${1:-}"
RUN_MIGRATE="${2:-}"

if [[ -z "$DB_URL" ]]; then
  echo "usage: $0 <database-url> [--migrate]" >&2
  exit 2
fi
if [[ -n "$RUN_MIGRATE" && "$RUN_MIGRATE" != "--migrate" ]]; then
  echo "unknown option: $RUN_MIGRATE" >&2
  exit 2
fi

command -v psql >/dev/null || { echo "psql not found" >&2; exit 1; }
if [[ -n "$RUN_MIGRATE" ]]; then
  command -v migrate >/dev/null || { echo "migrate CLI not found" >&2; exit 1; }
fi

SCHEMAS=(platform identity ops fleet booking finance notify review audit)

# Host and database name, read from the URL without printing the password.
HOST="$(sed -E 's#^[a-z]+://([^@]*@)?([^:/?]+).*#\2#' <<<"$DB_URL")"
DB_NAME="$(psql "$DB_URL" -Atc 'SELECT current_database()')"
CURRENT_VERSION="$(psql "$DB_URL" -Atc \
  "SELECT version || CASE WHEN dirty THEN ' (dirty)' ELSE '' END
     FROM public.schema_migrations" 2>/dev/null || echo 'none')"

echo "About to DELETE ALL SL-Bus data:"
echo "  host:              $HOST"
echo "  database:          $DB_NAME"
echo "  migration version: ${CURRENT_VERSION:-none}"
echo "  schemas dropped:   ${SCHEMAS[*]}"
echo "  also dropped:      public.schema_migrations"
echo
echo "Take a backup first if you might need this data (pg_dump)."
read -r -p "Type the host ($HOST) to continue: " ANSWER
if [[ "$ANSWER" != "$HOST" ]]; then
  echo "Host did not match. Nothing was changed."
  exit 1
fi

DROP_SQL="BEGIN;"
for s in "${SCHEMAS[@]}"; do
  DROP_SQL+=" DROP SCHEMA IF EXISTS \"$s\" CASCADE;"
done
DROP_SQL+=" DROP TABLE IF EXISTS public.schema_migrations; COMMIT;"

psql "$DB_URL" -v ON_ERROR_STOP=1 -q -c "$DROP_SQL"
echo "Database wiped."

if [[ -n "$RUN_MIGRATE" ]]; then
  SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
  migrate -path "$SCRIPT_DIR/../db/migrations" -database "$DB_URL" up
  echo "Migrations applied."
fi
