#!/usr/bin/env bash
# Run a local SimpleOTA server to develop and test the CLI against.
#
#   scripts/devserver.sh up      clone/update the server, migrate, seed, (re)start
#   scripts/devserver.sh down    stop it
#   scripts/devserver.sh reset   stop it and delete its database and uploads
#   scripts/devserver.sh env     print the API URL and test tokens
#
# The server runs from its own clone under .devserver/, with SQLite and local
# file storage, in an empty environment: no .env file, no production secrets,
# no object storage, no email, no analytics. Tokens are written to
# .devserver/env (0600); each `up` restarts the server and issues new ones.
#
# Settings (environment):
#   SIMPLEOTA_SERVER_REPO  git URL or local path to clone (default: GitHub)
#   SIMPLEOTA_SERVER_REF   branch, tag or commit to run (default: main)
#   SIMPLEOTA_DEV_PORT     port on 127.0.0.1 (default: 8765)
#   PYTHON                 Python 3.12+ interpreter (default: python3)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE="$ROOT/.devserver"
SRC="$STATE/simpleOTA"
VENV="$STATE/venv"
ENV_FILE="$STATE/env"
PID_FILE="$STATE/server.pid"
LOG_FILE="$STATE/server.log"
REPO="${SIMPLEOTA_SERVER_REPO:-https://github.com/wassie-dev/simpleOTA.git}"
REF="${SIMPLEOTA_SERVER_REF:-main}"
PORT="${SIMPLEOTA_DEV_PORT:-8765}"
PYTHON="${PYTHON:-python3}"
API_URL="http://127.0.0.1:$PORT"

die() { echo "devserver: $*" >&2; exit 1; }
log() { echo "devserver: $*" >&2; }

# The server runs with only the variables it needs, so nothing from the
# caller's shell (AWS keys, database settings) leaks in.
SERVER_ENV=(env -i
  HOME="$HOME" PATH="/usr/bin:/bin" LANG=C.UTF-8
  DEBUG=True
  SECRET_KEY=simpleota-cli-devserver-not-a-secret
  "ALLOWED_HOSTS=127.0.0.1,localhost"
  MAILER_EMAIL_BACKEND=django.core.mail.backends.console.EmailBackend
  INDEXNOW_ENABLED=False)

manage() {
  (cd "$SRC" && exec "${SERVER_ENV[@]}" "$VENV/bin/python" manage.py "$@")
}

healthy() {
  curl -fsS "$API_URL/api/health/" >/dev/null 2>&1
}

running() {
  [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null
}

checkout() {
  mkdir -p "$STATE"
  if [[ ! -d "$SRC/.git" ]]; then
    log "cloning $REPO"
    git clone --quiet "$REPO" "$SRC"
  else
    git -C "$SRC" fetch --quiet origin || log "fetch failed; using the existing checkout"
  fi
  if git -C "$SRC" rev-parse --verify --quiet "origin/$REF" >/dev/null; then
    git -C "$SRC" checkout --quiet --detach "origin/$REF"
  else
    git -C "$SRC" checkout --quiet --detach "$REF"
  fi
  log "server at $(git -C "$SRC" log -1 --format='%h %s')"
}

install() {
  "$PYTHON" -c 'import sys; sys.exit(sys.version_info < (3, 12))' ||
    die "$PYTHON is older than 3.12; set PYTHON to a newer interpreter"
  [[ -x "$VENV/bin/python" ]] || "$PYTHON" -m venv "$VENV"
  local want have=""
  want="$(shasum -a 256 "$SRC/requirements.txt" | cut -d' ' -f1)"
  [[ -f "$STATE/requirements.sha256" ]] && have="$(cat "$STATE/requirements.sha256")"
  if [[ "$want" != "$have" ]]; then
    log "installing server dependencies"
    "$VENV/bin/pip" install --quiet --disable-pip-version-check -r "$SRC/requirements.txt"
    echo "$want" >"$STATE/requirements.sha256"
  fi
}

seed() {
  manage migrate --verbosity 0
  manage bootstrap >/dev/null
  local out
  out="$(manage shell -c "$(cat "$ROOT/scripts/devserver_seed.py")")"
  (umask 077 && {
    echo "SIMPLEOTA_DEV_API_URL=$API_URL"
    grep '^SIMPLEOTA_DEV_' <<<"$out"
  } >"$ENV_FILE")
}

start() {
  healthy && die "something else is already serving $API_URL; stop it or set SIMPLEOTA_DEV_PORT"
  log "starting on $API_URL (log: $LOG_FILE)"
  (cd "$SRC" && exec "${SERVER_ENV[@]}" "$VENV/bin/python" manage.py runserver "127.0.0.1:$PORT" --noreload --no-css) >"$LOG_FILE" 2>&1 &
  echo $! >"$PID_FILE"
  for _ in $(seq 1 60); do
    healthy && return
    running || die "the server exited; see $LOG_FILE"
    sleep 1
  done
  die "the server did not become healthy in 60s; see $LOG_FILE"
}

stop() {
  if running; then
    kill "$(cat "$PID_FILE")"
    for _ in $(seq 1 20); do
      running || break
      sleep 0.5
    done
    log "stopped"
  fi
  rm -f "$PID_FILE"
}

show_env() {
  [[ -f "$ENV_FILE" ]] || die "not seeded yet; run scripts/devserver.sh up"
  cat "$ENV_FILE"
  cat >&2 <<EOF

Try the CLI against it:
  export SIMPLEOTA_API_URL=\$(grep SIMPLEOTA_DEV_API_URL $ENV_FILE | cut -d= -f2)
  grep SIMPLEOTA_DEV_PERSONAL_TOKEN $ENV_FILE | cut -d= -f2 | go run ./cmd/simpleota login
EOF
}

case "${1:-up}" in
  up) checkout; install; stop; seed; start; show_env ;;
  down) stop ;;
  reset) stop; rm -rf "${SRC:?}/db.sqlite3" "${SRC:?}/media" "$ENV_FILE"; log "reset" ;;
  env) show_env ;;
  *) die "usage: scripts/devserver.sh [up|down|reset|env]" ;;
esac
