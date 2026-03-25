#!/bin/bash
set -e

DATA_DIR="${DATA_DIR:-/data}"
BACKUP_DIR="${BACKUP_DIR:-/backups}"
BACKUP_INTERVAL="${BACKUP_INTERVAL:-3600}"
BACKUP_RETAIN="${BACKUP_RETAIN:-24}"
S3_BUCKET="${BACKUP_S3_BUCKET:-}"

mkdir -p "$BACKUP_DIR"

do_backup() {
    TIMESTAMP=$(date -u +"%Y%m%dT%H%M%SZ")
    echo "[${TIMESTAMP}] Starting backup..."

    for src in "$DATA_DIR"/*.tigerbeetle; do
        [ -f "$src" ] || continue
        FILENAME=$(basename "$src")
        DEST="$BACKUP_DIR/${TIMESTAMP}_${FILENAME}"
        cp "$src" "$DEST"
        echo "[${TIMESTAMP}] Backed up $FILENAME -> $DEST"

        if [ -n "$S3_BUCKET" ]; then
            aws s3 cp "$DEST" "s3://${S3_BUCKET}/tigerbeetle/${TIMESTAMP}_${FILENAME}"
            echo "[${TIMESTAMP}] Uploaded to s3://${S3_BUCKET}/..."
        fi
    done

    # Prune old local backups beyond BACKUP_RETAIN
    ls -t "$BACKUP_DIR"/*.tigerbeetle 2>/dev/null \
        | tail -n "+$((BACKUP_RETAIN + 1))" \
        | xargs -r rm -v

    echo "[${TIMESTAMP}] Backup complete."
}

echo "Backup sidecar started. interval=${BACKUP_INTERVAL}s retain=${BACKUP_RETAIN}"
while true; do
    do_backup
    sleep "$BACKUP_INTERVAL"
done
