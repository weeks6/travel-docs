#!/usr/bin/env bash
# Dumps the Travel Docs database (routes, their history, users, collections,
# trips…) together with the route scraper's state, into one archive to load
# on another machine with scripts/db-restore.sh.
#
# Usage: scripts/db-dump.sh [FILE]
#   FILE  where to write the archive (default: dumps/travel-docs-YYYYMMDD-HHMMSS.tar.gz)
#
# The archive holds:
#   db.dump                    pg_dump of the database (custom format, compressed)
#   scraper-state.volume.json  state of the scraper container (the scraper-state volume), if any
#   scraper-state.local.json   state of `make scrape` (scraper/data/state.json), if any
#   manifest.txt               when, from which schema version, how many routes and users
#
# The scraper state maps source trails to the routes they became, so that
# the scraper on the other machine updates those routes instead of
# importing everything again.
#
# Uses the Docker database (docker compose service "db"), which must be
# running. The dump contains users' emails and password hashes: keep it
# private.
set -euo pipefail

cd "$(dirname "$0")/.."

out="${1:-dumps/travel-docs-$(date +%Y%m%d-%H%M%S).tar.gz}"
mkdir -p "$(dirname "$out")"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
arch="$work/archive" # what goes in the archive
mkdir "$arch"

if ! docker compose exec -T db pg_isready -U travel -d travel_docs >/dev/null 2>&1; then
  echo "The database isn't running. Start it with: docker compose up -d db" >&2
  exit 1
fi

# The counts in the manifest and the dump come from one snapshot of the
# database, so they agree even while the API or the scraper keep writing:
# a session holds a read-only transaction, exports its snapshot, counts, and
# pg_dump reads the same snapshot.
mkfifo "$work/session.sql"
docker compose exec -T db psql -U travel -d travel_docs -Atq -v ON_ERROR_STOP=1 \
  <"$work/session.sql" >"$work/session.out" &
session=$!
exec 3>"$work/session.sql"
cat >&3 <<'SQL'
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SELECT pg_export_snapshot();
SELECT max(version) FROM schema_migrations;
SELECT count(*) FROM routes;
SELECT count(*) FROM users;
SELECT count(*) FROM collections;
SELECT count(*) FROM trips;
SQL
for _ in $(seq 300); do
  [ "$(wc -l <"$work/session.out")" -ge 6 ] && break
  kill -0 "$session" 2>/dev/null || break
  sleep 0.1
done
if [ "$(wc -l <"$work/session.out")" -lt 6 ]; then
  echo "Could not open a database session." >&2
  exit 1
fi
line() { sed -n "$1p" "$work/session.out"; }

echo "Dumping the database…"
# Inside the container, so the pg_dump version always matches the server.
docker compose exec -T db pg_dump -U travel -d travel_docs --snapshot="$(line 1)" \
  --format=custom --compress=9 >"$arch/db.dump"
echo "COMMIT;" >&3
exec 3>&-
wait "$session"

# Copied after the dump: if the scraper exported more meanwhile, the state
# names a few routes the dump lacks, and the scraper there imports them again.
echo "Copying the scraper state…"
project="$(docker compose config | awk '/^name:/ { print $2; exit }')"
volume="${project}_scraper-state"
if docker volume inspect "$volume" >/dev/null 2>&1; then
  docker pull -q alpine:3.20 >/dev/null
  # A throwaway container reads the volume (the scraper image has no shell).
  docker run --rm -v "$volume:/data:ro" alpine:3.20 sh -c 'cat /data/state.json 2>/dev/null' \
    >"$arch/scraper-state.volume.json" || true
  [ -s "$arch/scraper-state.volume.json" ] || rm -f "$arch/scraper-state.volume.json"
fi
if [ -s scraper/data/state.json ]; then
  cp scraper/data/state.json "$arch/scraper-state.local.json"
fi

{
  echo "Travel Docs database dump"
  echo "created:     $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "migration:   $(line 2)"
  echo "routes:      $(line 3)"
  echo "users:       $(line 4)"
  echo "collections: $(line 5)"
  echo "trips:       $(line 6)"
  echo "files:       $(cd "$arch" && ls | tr '\n' ' ')"
} >"$arch/manifest.txt"

tar -czf "$out" -C "$arch" .
cat "$arch/manifest.txt"
echo
echo "Wrote $out ($(du -h "$out" | cut -f1))."
echo "Copy it to the other machine (into a clone of this repository) and run: scripts/db-restore.sh $out"
