#!/usr/bin/env bash
# Loads an archive made by scripts/db-dump.sh: replaces the database with
# the dumped one and puts the route scraper's state back.
#
# Usage: scripts/db-restore.sh [--yes] FILE
#   --yes  don't ask for confirmation
#
# This REPLACES everything in the Docker database (docker compose service
# "db", started if needed). The API and the scraper are stopped while it
# runs and started again afterwards if they were running in Docker; an API
# you run yourself (scripts/dev.sh) reconnects on its own.
#
# Environment: DB_NAME (default travel_docs) restores into another database
# of the same server instead, e.g. to try a dump without touching your data.
set -euo pipefail

cd "$(dirname "$0")/.."

yes=false
if [ "${1:-}" = "--yes" ]; then
  yes=true
  shift
fi
file="${1:-}"
if [ -z "$file" ] || [ ! -f "$file" ]; then
  echo "Usage: scripts/db-restore.sh [--yes] FILE (an archive from scripts/db-dump.sh)" >&2
  exit 2
fi
db="${DB_NAME:-travel_docs}"
case "$db" in *[!a-z0-9_]* | "")
  echo "DB_NAME must be lowercase letters, digits and _" >&2
  exit 2
  ;;
esac

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
tar -xzf "$file" -C "$work"
if [ ! -f "$work/db.dump" ]; then
  echo "$file is not a Travel Docs dump (no db.dump inside)." >&2
  exit 1
fi
cat "$work/manifest.txt" 2>/dev/null || true
echo

if ! $yes; then
  read -r -p "Replace ALL data in database \"$db\" with this dump? Type yes to continue: " answer
  [ "$answer" = "yes" ] || { echo "Cancelled."; exit 1; }
fi

docker compose up -d --wait db >/dev/null

# Stop what writes to the database; remember it to start it again.
restart=()
if [ "$db" = "travel_docs" ]; then
  for svc in app scraper; do
    if [ -n "$(docker compose ps -q --status running "$svc" 2>/dev/null)" ]; then
      restart+=("$svc")
    fi
  done
  if [ ${#restart[@]} -gt 0 ]; then
    echo "Stopping ${restart[*]}…"
    docker compose stop "${restart[@]}" >/dev/null
  fi
fi

psql() { docker compose exec -T db psql -U travel -v ON_ERROR_STOP=1 -q "$@"; }

echo "Recreating database ${db}…"
# FORCE closes other connections (e.g. an API running outside Docker).
psql -d postgres -c "DROP DATABASE IF EXISTS $db WITH (FORCE)"
psql -d postgres -c "CREATE DATABASE $db OWNER travel"

echo "Loading the dump…"
docker compose exec -T db pg_restore -U travel -d "$db" --no-owner --role=travel --exit-on-error <"$work/db.dump"

routes="$(docker compose exec -T db psql -U travel -d "$db" -Atqc 'SELECT count(*) FROM routes')"
echo "Restored $routes routes."

if [ "$db" = "travel_docs" ]; then
  if [ -f "$work/scraper-state.volume.json" ]; then
    project="$(docker compose config | awk '/^name:/ { print $2; exit }')"
    volume="${project}_scraper-state"
    docker volume create "$volume" >/dev/null
    docker pull -q alpine:3.20 >/dev/null
    # 65532 is the scraper image's non-root user.
    docker run --rm -i -v "$volume:/data" alpine:3.20 \
      sh -c 'cat >/data/state.json && chown 65532:65532 /data /data/state.json' <"$work/scraper-state.volume.json"
    echo "Restored the scraper container's state."
  fi
  if [ -f "$work/scraper-state.local.json" ]; then
    mkdir -p scraper/data
    [ -f scraper/data/state.json ] && cp scraper/data/state.json "scraper/data/state.json.bak"
    cp "$work/scraper-state.local.json" scraper/data/state.json
    echo "Restored scraper/data/state.json (the previous one is in state.json.bak)."
  fi
else
  echo "Restored into $db: the scraper state was left alone."
fi

if [ ${#restart[@]} -gt 0 ]; then
  echo "Starting ${restart[*]}…"
  docker compose start "${restart[@]}" >/dev/null
fi
echo "Done."
