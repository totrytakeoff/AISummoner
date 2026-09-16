#!/bin/sh
# AISummoner ASD deployment smoke check (read-only, no secrets printed).
# Exit 0 when all checks pass, non-zero otherwise. Run as root.
set -u

PUBLIC_IP="${AISUMMONER_PUBLIC_IP:-122.51.70.33}"
PUBLIC_PORT="${AISUMMONER_PUBLIC_PORT:-10001}"
SERVER_PORT="${AISUMMONER_SERVER_PORT:-8088}"
SERVER_ENV="${AISUMMONER_SERVER_ENV:-/home/myself/.local/state/aisummoner-task011/20260821T090612Z/runtime/server.env}"
TLS_CERT="${AISUMMONER_TLS_CERT:-/home/myself/.local/state/aisummoner-task011/20260821T090612Z/tls/server.crt}"
CADDY_CONTAINER="${AISUMMONER_CADDY_CONTAINER:-aisummoner-task011-caddy-20260821t090612z}"
OPENCODE_CONTAINER="${AISUMMONER_OPENCODE_CONTAINER:-aisummoner-task011-opencode-20260821t090612z}"

fail=0
note() { printf '%s\n' "$*"; }
check() { # name, status(0=pass)
  if [ "$2" -eq 0 ]; then
    printf 'PASS  %s\n' "$1"
  else
    printf 'FAIL  %s\n' "$1"
    fail=1
  fi
}

listener_count() { # port -> count
  ss -ltnH 2>/dev/null | awk -v p="$1" '{ s=$4; sub(/^.*:/, "", s); if (s == p) c++ } END { print c + 0 }'
}

# 1. Server loopback health
curl -fsS --max-time 3 "http://127.0.0.1:$SERVER_PORT/healthz" >/dev/null 2>&1
check "server loopback /healthz" $?

# 2. HTTPS health through Caddy (loopback --resolve, strict cert)
curl -fsS --connect-timeout 3 --max-time 6 \
  --resolve "$PUBLIC_IP:$PUBLIC_PORT:127.0.0.1" \
  "https://$PUBLIC_IP:$PUBLIC_PORT/healthz" >/dev/null 2>&1
check "https /healthz via caddy" $?

# 3. Component listeners
for spec in "14196 DSH" "14197 bridge" "14096 opencode"; do
  port=${spec%% *}; name=${spec#* }
  n=$(listener_count "$port")
  [ "$n" -ge 1 ]
  check "listener $name :$port" $?
done

# 4. Exactly one server listener
n=$(listener_count "$SERVER_PORT")
[ "$n" -eq 1 ]
check "single listener :$SERVER_PORT (got $n)" $?

# 5. SQLite quick_check (read-only)
data_dir=$(sed -n 's/^AISUMMONER_DATA_DIR=//p' "$SERVER_ENV" | tr -d '\r\n')
db="$data_dir/aisummoner.db"
if [ -n "$data_dir" ] && [ -f "$db" ]; then
  res=""
  if command -v sqlite3 >/dev/null 2>&1; then
    res=$(sqlite3 "file:$db?mode=ro" "PRAGMA quick_check;" 2>/dev/null)
  elif command -v python3 >/dev/null 2>&1; then
    res=$(python3 -c "import sqlite3; print(sqlite3.connect('file:$db?mode=ro', uri=True).execute('PRAGMA quick_check').fetchone()[0])" 2>/dev/null)
  fi
  [ "$res" = "ok" ]
  check "sqlite quick_check" $?
else
  note "SKIP  sqlite quick_check (db not found at $db)"
fi

# 6. Containers running
for c in "$CADDY_CONTAINER" "$OPENCODE_CONTAINER"; do
  st=$(docker inspect -f '{{.State.Running}}' "$c" 2>/dev/null)
  [ "$st" = "true" ]
  check "container running $c" $?
done

# 7. Server listener owner is myself
spid=$(ss -ltnpH 2>/dev/null | awk -v p=":$SERVER_PORT" '$4 ~ p { match($0, /pid=[0-9]+/); if (RSTART) { print substr($0, RSTART+4, RLENGTH-4); exit } }')
u=$(ps -o user= -p "$spid" 2>/dev/null | tr -d ' ')
[ "$u" = "myself" ]
check "server process user (got '$u')" $?

# 8. Serving cert valid for more than 2 days
openssl x509 -in "$TLS_CERT" -noout -checkend 172800 >/dev/null 2>&1
check "serving cert valid >2 days" $?

note "smoke result: $([ "$fail" -eq 0 ] && echo PASS || echo FAIL)"
exit "$fail"
