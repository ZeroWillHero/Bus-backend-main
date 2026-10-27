#!/bin/sh
# db-backup service loop: sleep until $BACKUP_TIME (HH:MM in $TZ, default
# 02:30 Asia/Colombo), run backup.sh, repeat. The result of the last run goes
# to $BACKUP_DIR/.last-status, which the compose healthcheck reads — a failed
# backup turns the container "unhealthy" (and is logged with ERROR).
set -eu

BACKUP_TIME="${BACKUP_TIME:-02:30}"
BACKUP_DIR="${BACKUP_DIR:-/backups}"
STATUS="$BACKUP_DIR/.last-status"

log() { echo "$(date '+%Y-%m-%dT%H:%M:%S%z') db-backup: $*"; }

case "$BACKUP_TIME" in
  [0-2][0-9]:[0-5][0-9]) ;;
  *) log "ERROR: BACKUP_TIME must be HH:MM (got '$BACKUP_TIME')"; exit 1;;
esac

mkdir -p "$BACKUP_DIR"
# No run yet since this container started: count as healthy.
[ -f "$STATUS" ] || echo "ok $(date '+%Y-%m-%dT%H:%M:%S%z') (no run yet)" > "$STATUS"

if [ -n "${BACKUP_S3_BUCKET:-}" ] && ! command -v rclone >/dev/null; then
  log "off-site upload enabled; installing rclone"
  if ! { export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get install -y -qq --no-install-recommends rclone ca-certificates >/dev/null; }; then
    log "ERROR: could not install rclone; uploads will fail until fixed"
  fi
fi

log "scheduled daily at $BACKUP_TIME ($(date +%Z)), keeping ${BACKUP_KEEP:-14} dumps in $BACKUP_DIR"
while :; do
  now=$(date +%s)
  next=$(date -d "today $BACKUP_TIME" +%s)
  [ "$next" -gt "$now" ] || next=$(date -d "tomorrow $BACKUP_TIME" +%s)
  log "next backup at $(date -d "@$next" '+%Y-%m-%d %H:%M %Z')"
  sleep $((next - now))

  if /backup/backup.sh; then
    echo "ok $(date '+%Y-%m-%dT%H:%M:%S%z')" > "$STATUS"
  else
    echo "failed $(date '+%Y-%m-%dT%H:%M:%S%z')" > "$STATUS"
    log "ERROR: backup FAILED — see messages above; retrying at the next scheduled time"
  fi
done
