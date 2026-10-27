#!/bin/sh
# One nightly backup: pg_dump -Fc of the app database into $BACKUP_DIR, prune
# to the newest $BACKUP_KEEP dumps, optionally upload to S3-compatible storage.
# Exits non-zero on any failure. Called by entrypoint.sh; can also be run by
# hand: docker compose -f docker-compose.prod.yml exec db-backup /backup/backup.sh
#
# Connection comes from the standard libpq env vars (PGHOST, PGUSER,
# PGPASSWORD, PGDATABASE) that docker-compose.prod.yml sets.
set -eu
umask 077

BACKUP_DIR="${BACKUP_DIR:-/backups}"
BACKUP_KEEP="${BACKUP_KEEP:-14}"
DB="${PGDATABASE:-slbus}"

log() { echo "$(date '+%Y-%m-%dT%H:%M:%S%z') db-backup: $*"; }
fail() { log "ERROR: $*"; exit 1; }

case "$BACKUP_KEEP" in ''|*[!0-9]*|0) fail "BACKUP_KEEP must be a positive integer (got '$BACKUP_KEEP')";; esac
mkdir -p "$BACKUP_DIR" || fail "cannot create $BACKUP_DIR"

name="${DB}-$(date '+%Y%m%d-%H%M%S').dump"
tmp="$BACKUP_DIR/.${name}.partial"
trap 'rm -f "$tmp"' EXIT

log "starting pg_dump of '$DB' on ${PGHOST:-localhost} -> $BACKUP_DIR/$name"
if ! pg_dump -Fc --no-password -d "$DB" -f "$tmp"; then
  fail "pg_dump failed; no new backup written"
fi
# A custom-format dump that pg_restore can't list is useless — check it.
if ! pg_restore --list "$tmp" >/dev/null; then
  fail "pg_restore --list could not read the new dump; discarded"
fi
mv "$tmp" "$BACKUP_DIR/$name" || fail "could not move dump into place"
log "wrote $name ($(du -h "$BACKUP_DIR/$name" | cut -f1))"

# Prune: keep the newest $BACKUP_KEEP dumps (timestamped names sort by time).
ls -1 "$BACKUP_DIR" | grep -E "^${DB}-[0-9]{8}-[0-9]{6}\.dump\$" | sort -r \
  | tail -n +"$((BACKUP_KEEP + 1))" | while read -r old; do
    rm -f "$BACKUP_DIR/$old" && log "pruned $old"
  done

# Optional off-site copy (S3-compatible: AWS S3, Backblaze B2, Cloudflare R2).
if [ -n "${BACKUP_S3_BUCKET:-}" ]; then
  command -v rclone >/dev/null || fail "BACKUP_S3_BUCKET set but rclone is not installed"
  : "${AWS_ACCESS_KEY_ID:?AWS_ACCESS_KEY_ID required when BACKUP_S3_BUCKET is set}"
  : "${AWS_SECRET_ACCESS_KEY:?AWS_SECRET_ACCESS_KEY required when BACKUP_S3_BUCKET is set}"
  prefix="${BACKUP_S3_PREFIX:-}"
  [ -n "$prefix" ] && prefix="${prefix%/}/"
  dest=":s3:${BACKUP_S3_BUCKET}/${prefix}${name}"
  provider=Other
  [ -n "${BACKUP_S3_ENDPOINT:-}" ] || provider=AWS
  log "uploading $name to s3://${BACKUP_S3_BUCKET}/${prefix}${name}"
  # rclone reads the keys from AWS_* via --s3-env-auth. Off-site retention is
  # left to the bucket's lifecycle rules.
  if ! rclone copyto --s3-provider "$provider" --s3-env-auth \
        ${BACKUP_S3_ENDPOINT:+--s3-endpoint "$BACKUP_S3_ENDPOINT"} \
        ${BACKUP_S3_REGION:+--s3-region "$BACKUP_S3_REGION"} \
        --s3-no-check-bucket --retries 3 \
        "$BACKUP_DIR/$name" "$dest"; then
    fail "off-site upload failed (local dump $name is kept)"
  fi
  log "upload ok"
fi

log "backup complete"
