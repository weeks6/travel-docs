#!/usr/bin/env bash
# Runs Travel Docs locally: PostgreSQL/PostGIS in Docker, the app (API + web UI)
# on your machine, with live reload when `air` is installed.
#
# Usage: scripts/dev.sh [--seed] [--open] [--no-reload]
#   --seed       create a demo account (demo@example.com / demo-password) with a sample route
#   --open       open the web UI in your browser once the app is up
#   --no-reload  build and run once, even if air is installed
#
# Environment: PORT (default 8080). Set DATABASE_URL to use your own database
# instead of the Docker one. Press Ctrl+C to stop the app (the database keeps
# running; stop it with `docker compose stop db`).
set -euo pipefail

cd "$(dirname "$0")/.."

PORT="${PORT:-8080}"
BASE="http://localhost:$PORT"
DEMO_EMAIL="demo@example.com"
DEMO_PASSWORD="demo-password"
SEED=0
OPEN=0
RELOAD=1

for arg in "$@"; do
  case "$arg" in
    --seed) SEED=1 ;;
    --open) OPEN=1 ;;
    --no-reload) RELOAD=0 ;;
    -h | --help) sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $arg (see --help)" >&2; exit 2 ;;
  esac
done

log() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

command -v go >/dev/null || die "Go is not installed"
command -v curl >/dev/null || die "curl is not installed"

# --- Database --------------------------------------------------------------

if [[ -n "${DATABASE_URL:-}" ]]; then
  log "Using DATABASE_URL from the environment (not starting the Docker database)"
else
  command -v docker >/dev/null || die "Docker is not installed"
  # Docker Desktop on macOS may only be reachable through its own context.
  if ! docker info >/dev/null 2>&1; then
    if DOCKER_CONTEXT=desktop-linux docker info >/dev/null 2>&1; then
      export DOCKER_CONTEXT=desktop-linux
    elif [[ "$(uname)" == Darwin && -d /Applications/Docker.app ]]; then
      log "Starting Docker Desktop…"
      open -a Docker
      for _ in $(seq 1 90); do
        DOCKER_CONTEXT=desktop-linux docker info >/dev/null 2>&1 && break
        sleep 2
      done
      DOCKER_CONTEXT=desktop-linux docker info >/dev/null 2>&1 || die "Docker did not start in time"
      export DOCKER_CONTEXT=desktop-linux
    else
      die "Docker is not running. Start it and try again."
    fi
  fi
  log "Starting the database…"
  docker compose up -d --wait db
  export DATABASE_URL="postgres://travel:travel@localhost:5432/travel_docs?sslmode=disable"
fi

# --- App -------------------------------------------------------------------

if pid=$(lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -t 2>/dev/null | head -1) && [[ -n "$pid" ]]; then
  die "port $PORT is already used by $(ps -o comm= -p "$pid") (pid $pid). Stop it or run with PORT=<other port>."
fi

export ADDR=":$PORT"
mkdir -p tmp
if [[ $RELOAD == 1 ]] && command -v air >/dev/null; then
  log "Starting the app with live reload (air): Go, HTML, JS, CSS and SQL changes rebuild it"
  air \
    -build.cmd "go build -o ./tmp/main ." \
    -build.bin "./tmp/main" \
    -build.include_ext "go,html,js,css,sql" \
    -build.exclude_dir "tmp,apitest,.git,.github,node_modules" &
else
  [[ $RELOAD == 1 ]] && log "air not found (go install github.com/air-verse/air@latest for live reload)"
  log "Building the app…"
  go build -o ./tmp/main .
  ./tmp/main &
fi
APP_PID=$!

cleanup() {
  trap - EXIT INT TERM
  kill "$APP_PID" 2>/dev/null || true
  wait "$APP_PID" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM

log "Waiting for the app to be ready…"
for _ in $(seq 1 120); do
  curl -fsS "$BASE/healthz" >/dev/null 2>&1 && break
  kill -0 "$APP_PID" 2>/dev/null || die "the app exited during startup (see output above)"
  sleep 1
done
curl -fsS "$BASE/healthz" >/dev/null 2>&1 || die "the app did not become ready on $BASE"

# --- Demo data -------------------------------------------------------------

json_field() { sed -n "s/.*\"$1\":\"\{0,1\}\([^\",}]*\).*/\1/p"; }

seed() {
  local body resp token total code
  body="{\"email\":\"$DEMO_EMAIL\",\"password\":\"$DEMO_PASSWORD\",\"display_name\":\"Demo\"}"
  resp=$(curl -sS -X POST "$BASE/auth/register" -H 'Content-Type: application/json' -d "$body")
  if ! grep -q '"access_token"' <<<"$resp"; then # already registered: log in
    resp=$(curl -sS -X POST "$BASE/auth/login" -H 'Content-Type: application/json' \
      -d "{\"email\":\"$DEMO_EMAIL\",\"password\":\"$DEMO_PASSWORD\"}")
  fi
  token=$(json_field access_token <<<"$resp")
  [[ -n "$token" ]] || { echo "  could not log in as $DEMO_EMAIL: $resp" >&2; return 1; }

  total=$(curl -sS -H "Authorization: Bearer $token" "$BASE/routes?scope=mine&page_size=1" | json_field total)
  if [[ "$total" == 0 ]]; then
    code=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token" \
      -F file=@gpx/testdata/track.gpx -F name="Sample loop" -F visibility=public "$BASE/routes")
    [[ "$code" == 201 ]] || { echo "  sample route upload failed (HTTP $code)" >&2; return 1; }
    log "Demo data ready: $DEMO_EMAIL with a public sample route"
  else
    log "Demo account already has routes; nothing to add"
  fi
}

[[ $SEED == 1 ]] && seed

cat <<EOF

  Travel Docs is running

  Web UI        $BASE
  Health        $BASE/healthz
  Public API    curl -s '$BASE/routes?scope=public'
  Database      docker compose exec db psql -U travel travel_docs
EOF
if [[ $SEED == 1 ]]; then
  cat <<EOF
  Demo login    $DEMO_EMAIL / $DEMO_PASSWORD
  API as demo   TOKEN=\$(curl -s -X POST $BASE/auth/login -H 'Content-Type: application/json' \\
                  -d '{"email":"$DEMO_EMAIL","password":"$DEMO_PASSWORD"}' | sed -n 's/.*"access_token":"\\([^"]*\\)".*/\\1/p')
                curl -s -H "Authorization: Bearer \$TOKEN" '$BASE/routes'
EOF
fi
printf '\n  Press Ctrl+C to stop.\n\n'

if [[ $OPEN == 1 ]]; then
  if command -v open >/dev/null; then open "$BASE"; elif command -v xdg-open >/dev/null; then xdg-open "$BASE"; fi
fi

wait "$APP_PID"
