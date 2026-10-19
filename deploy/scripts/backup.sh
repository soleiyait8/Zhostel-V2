#!/usr/bin/env bash
# Writes a compressed, verified PostgreSQL dump to $BACKUP_DIR and prunes old
# ones. Run from deploy/. Schedule it daily, e.g. in root's crontab:
#   15 2 * * *  cd /opt/adms/deploy && ./scripts/backup.sh >> backups/backup.log 2>&1
set -euo pipefail
cd "$(dirname "$0")/.."
set -a; [ -f .env ] && . ./.env; set +a

DIR="${BACKUP_DIR:-./backups}"
KEEP="${BACKUP_KEEP_DAYS:-30}"
USER_="${POSTGRES_USER:-adms}"
DB="${POSTGRES_DB:-adms}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="$DIR/adms-$STAMP.dump"

mkdir -p "$DIR"
chmod 700 "$DIR"
umask 077

echo "[$(date -u +%FT%TZ)] backing up $DB to $OUT"
docker compose exec -T postgres pg_dump -U "$USER_" -d "$DB" --format=custom --compress=6 > "$OUT.partial"

# A dump that pg_restore cannot list is not a backup.
docker compose exec -T postgres pg_restore --list < "$OUT.partial" > /dev/null
mv "$OUT.partial" "$OUT"
sha256sum "$OUT" > "$OUT.sha256"
echo "[$(date -u +%FT%TZ)] ok: $(du -h "$OUT" | cut -f1)"

find "$DIR" -name 'adms-*.dump*' -type f -mtime +"$KEEP" -print -delete
echo "Copy $DIR off this server (object storage, another host). A backup on the same disk is not a backup."
