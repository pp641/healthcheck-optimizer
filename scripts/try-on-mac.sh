#!/bin/sh
# One command to try both Tidyfleet apps on this Mac: starts the company
# dashboard (Docker), creates an organization (once), enrolls this Mac, then
# opens the cleaner app. Safe to re-run. Nothing is cleaned unless you choose
# to in the cleaner.
set -eu
cd "$(dirname "$0")/.."

API_PORT=${TIDYFLEET_API_PORT:-8088} # 8080 is often taken (Jenkins etc.)
DASH_PORT=${TIDYFLEET_DASHBOARD_PORT:-3000}
export TIDYFLEET_API_PORT=$API_PORT TIDYFLEET_DASHBOARD_PORT=$DASH_PORT
API=http://127.0.0.1:$API_PORT
CREDS=.tidyfleet-dev
BIN=bin/tidyfleet-darwin-$(uname -m | sed 's/x86_64/amd64/')

docker info >/dev/null 2>&1 || { echo "Docker is not running. Start Docker Desktop and try again." >&2; exit 1; }

echo "Building the agent and cleaner app…"
make agent-darwin >/dev/null

echo "Starting Postgres, API (:$API_PORT) and dashboard (:$DASH_PORT)…"
docker compose up -d --build --quiet-pull >/dev/null 2>&1 || docker compose up -d --build
i=0
until curl -fsS "$API/healthz" >/dev/null 2>&1; do
  i=$((i + 1)); [ $i -gt 60 ] && { echo "API did not start; see: docker compose logs server" >&2; exit 1; }
  sleep 1
done

login_ok() {
  [ -f "$CREDS" ] || return 1
  . "./$CREDS"
  curl -fsS -o /dev/null -X POST "$API/api/v1/auth/login" \
    -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}" 2>/dev/null
}

if ! login_ok; then
  EMAIL=admin@tidyfleet.local
  PASSWORD=$(openssl rand -base64 18 | tr -d '/+=' | cut -c1-16)
  out=$(docker compose exec -T server tidyfleet-server create-org -name "My Team" -admin-email "$EMAIL" -admin-password "$PASSWORD")
  CODE=$(printf '%s\n' "$out" | awk '/Enrollment code:/ {print $3}')
  umask 077
  printf 'EMAIL=%s\nPASSWORD=%s\nCODE=%s\n' "$EMAIL" "$PASSWORD" "$CODE" > "$CREDS"
fi
. "./$CREDS"

enroll() { "$BIN" enroll "$API" "$CODE" --yes >/dev/null; }
if ! "$BIN" config show | grep -q "managed by"; then
  echo "Enrolling this Mac…"
  enroll
fi

echo "Scanning for reclaimable dev space (read-only)…"
"$BIN" scan >/dev/null 2>&1 || true
if ! "$BIN" report >/dev/null 2>&1; then
  enroll # the server was reset since this Mac enrolled
  "$BIN" report >/dev/null
fi

open "http://localhost:$DASH_PORT" 2>/dev/null || true
cat <<EOF

1) Company dashboard (keeps running in Docker)
     http://localhost:$DASH_PORT
     Email:     $EMAIL
     Password:  $PASSWORD      (saved in $CREDS)
     Stop it:   make down

2) Cleaner app (this Mac): opening now. Close it with Ctrl+C.
EOF
exec "$BIN" ui
