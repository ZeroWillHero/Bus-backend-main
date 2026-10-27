#!/bin/sh
# Restore a pg_dump -Fc file into a target database.
#
#   CONFIRM_RESTORE=yes restore.sh <dump-file> <target-database-url>
#
# --clean --if-exists drops and recreates every object in the dump, so this
# OVERWRITES the target's data. --no-owner makes the connecting user own the
# restored objects. It runs in one transaction and stops at the first error,
# so a failed restore leaves the target unchanged. Grants to slbus_app are in
# the dump, so that role must already exist in the target (prod's
# postgres-init creates it) or the restore fails.
set -eu

usage() { echo "usage: CONFIRM_RESTORE=yes $0 <dump-file> <target-database-url>" >&2; exit 2; }
[ $# -eq 2 ] || usage
dump=$1
target=$2

[ -r "$dump" ] || { echo "restore: cannot read dump '$dump'" >&2; exit 2; }
if [ "${CONFIRM_RESTORE:-}" != "yes" ]; then
  echo "restore: refusing to run. This overwrites the target database." >&2
  echo "restore: re-run with CONFIRM_RESTORE=yes once you are sure of the target." >&2
  exit 3
fi

pg_restore --list "$dump" >/dev/null || { echo "restore: '$dump' is not a readable custom-format dump" >&2; exit 2; }

# Print the target without its password.
safe=$(printf '%s' "$target" | sed -E 's#(://[^:/@]+):[^@]*@#\1:***@#')
echo "restore: restoring $dump into $safe"
pg_restore --clean --if-exists --no-owner --single-transaction --exit-on-error \
  -d "$target" "$dump"
echo "restore: done"
