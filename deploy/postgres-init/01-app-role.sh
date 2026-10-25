#!/usr/bin/env bash
# First-start only (docker-entrypoint-initdb.d). Creates the API's
# non-superuser role with the real password; migration 000005 then sees it
# exists and skips its own CREATE ROLE (which uses a default password).
set -euo pipefail

if [[ -z "${SLBUS_APP_PASSWORD:-}" ]]; then
  echo "SLBUS_APP_PASSWORD is required" >&2
  exit 1
fi

psql -v ON_ERROR_STOP=1 -v pw="$SLBUS_APP_PASSWORD" \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<'SQL'
CREATE ROLE slbus_app LOGIN PASSWORD :'pw'
    NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
SQL
