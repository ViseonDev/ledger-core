#!/usr/bin/env bash
# Nightly logical backup of the ledger database.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

COMPOSE_DIR="${COMPOSE_DIR:-$REPO_DIR}"
BACKUP_DIR="${BACKUP_DIR:-$COMPOSE_DIR/deploy/backups}"
RETENTION_DAYS="${RETENTION_DAYS:-14}"
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
TARGET="${BACKUP_DIR}/ledger-${STAMP}.dump"

cd "$COMPOSE_DIR"

if [[ -f .env.production ]]; then
    # shellcheck disable=SC1091
    source .env.production
    ENV_FILE=".env.production"
elif [[ -f .env ]]; then
    # shellcheck disable=SC1091
    source .env
    ENV_FILE=".env"
else
    echo "neither .env.production nor .env found in $COMPOSE_DIR" >&2
    exit 1
fi

mkdir -p "$BACKUP_DIR"

COMPOSE_FILES=("-f" "docker-compose.yml")
if [[ -f docker-compose.prod.yml ]]; then
    COMPOSE_FILES+=("-f" "docker-compose.prod.yml")
fi

# --format=custom is compressed and allows selective restore with pg_restore.
docker compose "${COMPOSE_FILES[@]}" \
  --env-file "$ENV_FILE" \
  exec -T postgres \
  pg_dump --username "${POSTGRES_USER:-ledger}" --dbname "${POSTGRES_DB:-ledger}" \
          --format=custom --compress=9 \
  > "$TARGET"

# Fail on an empty dump.
if [[ ! -s "$TARGET" ]]; then
    echo "backup produced an empty file: $TARGET" >&2
    rm -f "$TARGET"
    exit 1
fi

# pg_restore --list fails on a truncated or corrupt dump.
if ! docker compose "${COMPOSE_FILES[@]}" \
        --env-file "$ENV_FILE" \
        exec -T postgres pg_restore --list /dev/stdin < "$TARGET" > /dev/null; then
    echo "backup failed verification: $TARGET" >&2
    exit 1
fi

chmod 600 "$TARGET"
echo "backup ok: $TARGET ($(du -h "$TARGET" | cut -f1))"

find "$BACKUP_DIR" -name 'ledger-*.dump' -type f -mtime "+${RETENTION_DAYS}" -delete
