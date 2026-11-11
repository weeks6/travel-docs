#!/usr/bin/env bash
# Runs Travel Docs locally: PostgreSQL/PostGIS in Docker, the API on your
# machine (live reload when `air` is installed) and the Next.js web UI
# (`next dev` in frontend/).
#
# Usage: scripts/dev.sh [--seed] [--open] [--no-reload] [--no-web]
#   --seed       create a demo account (demo@example.com / demo-password) with a sample route
#   --open       open the web UI in your browser once everything is up
#   --no-reload  build and run the API once, even if air is installed
#   --no-web     run only the API
#
# Environment: PORT (API, default 8080), WEB_PORT (web UI, default 3000),
# DB_PORT (host port of the Docker database, default 5432; change it if a local
# PostgreSQL already uses 5432). Set DATABASE_URL to use your own database
# instead of the Docker one. Press Ctrl+C to stop (the database keeps running;
# `docker compose stop db`). On Windows, run it from Git Bash.
set -euo pipefail

cd "$(dirname "$0")/.."

PORT="${PORT:-8080}"
export DB_PORT="${DB_PORT:-5432}"
# Go adds .exe to binaries on Windows (Git Bash reports MINGW/MSYS).
case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) EXE=.exe ;; *) EXE= ;; esac
WEB_PORT="${WEB_PORT:-3000}"
BASE="http://localhost:$PORT"
WEB="http://localhost:$WEB_PORT"
DEMO_EMAIL="demo@example.com"
DEMO_PASSWORD="demo-password"
SEED=0
OPEN=0
RELOAD=1
WEB_UI=1

for arg in "$@"; do
  case "$arg" in
    --seed) SEED=1 ;;
    --open) OPEN=1 ;;
    --no-reload) RELOAD=0 ;;
    --no-web) WEB_UI=0 ;;
    -h | --help) sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
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
  docker compose up -d --wait db || die "could not start the database. If port $DB_PORT is taken (e.g. by a local PostgreSQL), run with DB_PORT=55432"
  export DATABASE_URL="postgres://travel:travel@localhost:$DB_PORT/travel_docs?sslmode=disable"
fi

# --- App -------------------------------------------------------------------

if pid=$(lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -t 2>/dev/null | head -1) && [[ -n "$pid" ]]; then
  die "port $PORT is already used by $(ps -o comm= -p "$pid") (pid $pid). Stop it or run with PORT=<other port>."
fi

export ADDR=":$PORT"
export CORS_ORIGINS="${CORS_ORIGINS:-$WEB}"
export LOG_FORMAT="${LOG_FORMAT:-text}"
export METRICS_ADDR="${METRICS_ADDR:-:9090}"
mkdir -p tmp
if [[ $RELOAD == 1 ]] && command -v air >/dev/null; then
  log "Starting the API with live reload (air): Go and SQL changes rebuild it"
  air \
    -build.cmd "go build -o ./tmp/main$EXE ." \
    -build.bin "./tmp/main$EXE" \
    -build.include_ext "go,sql" \
    -build.exclude_dir "tmp,apitest,.git,.github,node_modules,frontend" &
else
  [[ $RELOAD == 1 ]] && log "air not found (go install github.com/air-verse/air@latest for live reload)"
  log "Building the app…"
  go build -o "./tmp/main$EXE" .
  "./tmp/main$EXE" &
fi
APP_PID=$!
WEB_PID=""

cleanup() {
  trap - EXIT INT TERM
  if [[ -n "$WEB_PID" ]]; then kill "$WEB_PID" 2>/dev/null || true; fi
  kill "$APP_PID" 2>/dev/null || true
  wait 2>/dev/null || true
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

# --- Web UI ----------------------------------------------------------------

if [[ $WEB_UI == 1 ]]; then
  command -v npm >/dev/null || die "Node.js/npm is not installed (or run with --no-web)"
  if [[ ! -d frontend/node_modules ]]; then
    log "Installing web UI dependencies…"
    (cd frontend && npm ci)
  fi
  log "Starting the web UI (Next.js dev server)…"
  (cd frontend && NEXT_PUBLIC_API_URL="$BASE" NEXT_TELEMETRY_DISABLED=1 exec npx next dev -p "$WEB_PORT") &
  WEB_PID=$!
  for _ in $(seq 1 120); do
    curl -fsS -o /dev/null "$WEB" 2>/dev/null && break
    kill -0 "$WEB_PID" 2>/dev/null || die "the web UI exited during startup (see output above)"
    sleep 1
  done
fi

cat <<EOF

  Travel Docs is running

  Web UI        $([[ $WEB_UI == 1 ]] && echo "$WEB" || echo "not started (--no-web)")
  API           $BASE
  Health        $BASE/healthz
  Metrics       http://localhost${METRICS_ADDR}/metrics
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
  URL=$([[ $WEB_UI == 1 ]] && echo "$WEB" || echo "$BASE")
  if [[ -n "$EXE" ]]; then start "" "$URL"
  elif command -v open >/dev/null; then open "$URL"
  elif command -v xdg-open >/dev/null; then xdg-open "$URL"; fi
fi

wait "$APP_PID"
