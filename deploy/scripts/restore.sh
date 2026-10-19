#!/usr/bin/env bash
# Restores a dump made by backup.sh, replacing the current database.
#   ./scripts/restore.sh backups/adms-20261019T021500Z.dump --yes
# Devices keep their unacknowledged records while the ADMS service is stopped
# and resend them afterwards; nothing they hold is lost during a restore.
set -euo pipefail
cd "$(dirname "$0")/.."
set -a; [ -f .env ] && . ./.env; set +a

FILE="${1:-}"
[ -f "$FILE" ] || { echo "usage: $0 <dump-file> --yes"; exit 2; }
[ "${2:-}" = "--yes" ] || { echo "This REPLACES all data in the database. Re-run with --yes to continue."; exit 2; }
if [ -f "$FILE.sha256" ]; then sha256sum -c "$FILE.sha256"; fi

USER_="${POSTGRES_USER:-adms}"
DB="${POSTGRES_DB:-adms}"

echo "Stopping services that write to the database"
docker compose stop api adms
echo "Restoring $FILE"
# Extensions are owned by the database superuser and already exist; restoring
# them as the application user fails and would roll back the whole restore,
# so they are filtered out of the table of contents.
docker compose cp "$FILE" postgres:/tmp/restore.dump
if ! docker compose exec -T postgres sh -c "
    pg_restore -l /tmp/restore.dump | grep -v ' EXTENSION ' > /tmp/restore.list &&
    pg_restore -U '$USER_' -d '$DB' --clean --if-exists --no-owner --single-transaction --exit-on-error \
      -L /tmp/restore.list /tmp/restore.dump"; then
  docker compose exec -T postgres rm -f /tmp/restore.dump /tmp/restore.list
  echo "Restore FAILED. The database was left unchanged (single transaction). Restarting services."
  docker compose start adms api
  exit 1
fi
docker compose exec -T postgres rm -f /tmp/restore.dump /tmp/restore.list
echo "Applying any newer migrations"
docker compose run --rm migrate
docker compose start adms api
echo "Restore complete. Check the dashboard and the Devices page."
