#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
  echo "usage: $0 RUN_ID REVIEWED_SERVER_BINARY TLS_INBOX" >&2
  exit 2
fi

run_id=$1
server_source=$2
tls_inbox=$3
public_host=122-51-70-33.sslip.io
public_port=10001
server_port=8088
run_user=myself
run_group=myself

case "$run_id" in
  20??????T??????Z) ;;
  *) echo "invalid run id" >&2; exit 1 ;;
esac

[ "$(id -u)" -eq 0 ] || {
  echo "ASD provisioning must be run by root" >&2
  exit 1
}
[ -x "$server_source" ] || {
  echo "reviewed Server binary is unavailable" >&2
  exit 1
}
for file in admin-password.txt ca.crt server.crt server.key; do
  [ -f "$tls_inbox/$file" ] || {
    echo "TLS inbox is incomplete" >&2
    exit 1
  }
done

listener_exists() {
  ss -ltnH | awk -v port="$1" '
    {
      address = $4
      sub(/^.*:/, "", address)
      if (address == port) found = 1
    }
    END { exit(found ? 0 : 1) }
  '
}

if listener_exists "$server_port" || listener_exists "$public_port"; then
  echo "required ASD listener is already occupied" >&2
  exit 1
fi

opt_root=/home/$run_user/.local/opt/aisummoner-task011
state_root=/home/$run_user/.local/state/aisummoner-task011
opt_dir=$opt_root/$run_id
state_dir=$state_root/$run_id
runtime_dir=$state_dir/runtime
tls_dir=$state_dir/tls
logs_dir=$state_dir/logs
data_dir=$state_dir/data
caddy_data=$state_dir/caddy-data
caddy_config=$state_dir/caddy-config
runtime_unit=aisummoner-task011-server-$run_id.service
bootstrap_unit=aisummoner-task011-bootstrap-$run_id.service
container_name=aisummoner-task011-caddy-$(printf '%s' "$run_id" | tr 'A-Z' 'a-z')

[ ! -e "$opt_dir" ] && [ ! -e "$state_dir" ] || {
  echo "run-owned directory already exists" >&2
  exit 1
}
if docker container inspect "$container_name" >/dev/null 2>&1; then
  echo "run-owned Caddy container already exists" >&2
  exit 1
fi

install -d -m 0700 -o "$run_user" -g "$run_group" \
  "$opt_root" "$state_root" "$opt_dir" "$opt_dir/bin" \
  "$state_dir" "$runtime_dir" "$tls_dir" "$logs_dir" "$data_dir" \
  "$caddy_data" "$caddy_config"
install -m 0700 -o "$run_user" -g "$run_group" \
  "$server_source" "$opt_dir/bin/aisummoner-server"
install -m 0600 -o "$run_user" -g "$run_group" \
  "$tls_inbox/ca.crt" "$tls_dir/ca.crt"
install -m 0600 -o "$run_user" -g "$run_group" \
  "$tls_inbox/server.crt" "$tls_dir/server.crt"
install -m 0600 -o "$run_user" -g "$run_group" \
  "$tls_inbox/server.key" "$tls_dir/server.key"

admin_password=$(tr -d '\r\n' < "$tls_inbox/admin-password.txt")
printf '%s' "$admin_password" | grep -Eq '^[0-9a-f]{64}$' || {
  echo "bootstrap password has an invalid format" >&2
  exit 1
}
session_secret=$(openssl rand -hex 32)
pairing_secret=$(openssl rand -hex 32)
base_url=https://$public_host:$public_port

bootstrap_env=$runtime_dir/bootstrap.env
runtime_env=$runtime_dir/server.env
umask 077
{
  printf 'AISUMMONER_BASE_URL=%s\n' "$base_url"
  printf 'AISUMMONER_LISTEN_ADDR=127.0.0.1:%s\n' "$server_port"
  printf 'AISUMMONER_TRUSTED_PROXY_IPS=127.0.0.1\n'
  printf 'AISUMMONER_DATA_DIR=%s\n' "$data_dir"
  printf 'AISUMMONER_DEV_MODE=0\n'
  printf 'AISUMMONER_ADMIN_PASSWORD=%s\n' "$admin_password"
  printf 'AISUMMONER_SESSION_SECRET=%s\n' "$session_secret"
  printf 'AISUMMONER_PAIRING_SECRET=%s\n' "$pairing_secret"
  printf 'AISUMMONER_AGENT_ADAPTER=fake\n'
} > "$bootstrap_env"
{
  printf 'AISUMMONER_BASE_URL=%s\n' "$base_url"
  printf 'AISUMMONER_LISTEN_ADDR=127.0.0.1:%s\n' "$server_port"
  printf 'AISUMMONER_TRUSTED_PROXY_IPS=127.0.0.1\n'
  printf 'AISUMMONER_DATA_DIR=%s\n' "$data_dir"
  printf 'AISUMMONER_DEV_MODE=0\n'
  printf 'AISUMMONER_SESSION_SECRET=%s\n' "$session_secret"
  printf 'AISUMMONER_PAIRING_SECRET=%s\n' "$pairing_secret"
  printf 'AISUMMONER_AGENT_ADAPTER=fake\n'
} > "$runtime_env"

caddy_file=$runtime_dir/Caddyfile
{
  printf '{\n'
  printf '  admin off\n'
  printf '  auto_https off\n'
  printf '}\n\n'
  printf 'https://%s:%s {\n' "$public_host" "$public_port"
  printf '  tls /tls/server.crt /tls/server.key\n'
  printf '  reverse_proxy 127.0.0.1:%s {\n' "$server_port"
  printf '    header_up X-AISummoner-Client-IP {remote_host}\n'
  printf '    flush_interval -1\n'
  printf '  }\n'
  printf '}\n'
} > "$caddy_file"
chown "$run_user:$run_group" "$bootstrap_env" "$runtime_env" "$caddy_file"
chmod 0600 "$bootstrap_env" "$runtime_env" "$caddy_file"

start_server_unit() {
  unit=$1
  environment_file=$2
  restart_policy=$3
  log_file=$4
  systemd-run --quiet --unit="$unit" --uid="$run_user" --gid="$run_group" \
    --working-directory="$opt_dir" \
    --property="EnvironmentFile=$environment_file" \
    --property="Restart=$restart_policy" \
    --property="RestartSec=2s" \
    --property="TimeoutStopSec=20s" \
    --property="KillMode=control-group" \
    --property="UMask=0077" \
    --property="StandardOutput=append:$log_file" \
    --property="StandardError=append:$log_file" \
    "$opt_dir/bin/aisummoner-server"
}

wait_for_server() {
  unit=$1
  attempts=0
  while [ "$attempts" -lt 100 ]; do
    if curl -fsS --max-time 1 "http://127.0.0.1:$server_port/healthz" >/dev/null 2>&1; then
      return 0
    fi
    systemctl is-active --quiet "$unit" || return 1
    attempts=$((attempts + 1))
    sleep 0.2
  done
  return 1
}

start_server_unit "$bootstrap_unit" "$bootstrap_env" no "$logs_dir/server-bootstrap.log"
if ! wait_for_server "$bootstrap_unit"; then
  echo "bootstrap Server did not become ready" >&2
  exit 1
fi
systemctl stop "$bootstrap_unit"
systemctl reset-failed "$bootstrap_unit" >/dev/null 2>&1 || true
truncate -s 0 "$bootstrap_env" "$tls_inbox/admin-password.txt"
unlink "$bootstrap_env"
unlink "$tls_inbox/admin-password.txt"
unset admin_password

start_server_unit "$runtime_unit" "$runtime_env" on-failure "$logs_dir/server.log"
if ! wait_for_server "$runtime_unit"; then
  echo "runtime Server did not become ready" >&2
  exit 1
fi

docker run --rm --network host --user 1001:1001 --read-only \
  -v "$caddy_file:/etc/caddy/Caddyfile:ro" \
  -v "$tls_dir:/tls:ro" \
  -v "$caddy_data:/data" \
  -v "$caddy_config:/config" \
  caddy:2.10.2-alpine caddy validate --config /etc/caddy/Caddyfile >/dev/null

container_id=$(docker run -d --name "$container_name" --network host \
  --user 1001:1001 --read-only --restart=no \
  --label com.aisummoner.scope=task011 \
  --label com.aisummoner.run_id="$run_id" \
  -v "$caddy_file:/etc/caddy/Caddyfile:ro" \
  -v "$tls_dir:/tls:ro" \
  -v "$caddy_data:/data" \
  -v "$caddy_config:/config" \
  caddy:2.10.2-alpine caddy run --config /etc/caddy/Caddyfile --adapter caddyfile)

attempts=0
while [ "$attempts" -lt 100 ]; do
  if curl -fsS --max-time 2 --cacert "$tls_dir/ca.crt" \
    --resolve "$public_host:$public_port:127.0.0.1" \
    "$base_url/healthz" >/dev/null 2>&1; then
    break
  fi
  [ "$(docker inspect -f '{{.State.Running}}' "$container_id")" = true ] || {
    echo "Caddy exited before TLS health succeeded" >&2
    exit 1
  }
  attempts=$((attempts + 1))
  sleep 0.2
done
[ "$attempts" -lt 100 ] || {
  echo "TLS endpoint did not become ready" >&2
  exit 1
}

{
  printf 'run_id=%s\n' "$run_id"
  printf 'server_unit=%s\n' "$runtime_unit"
  printf 'caddy_container=%s\n' "$container_name"
  printf 'caddy_container_id=%s\n' "$container_id"
  printf 'public_url=%s\n' "$base_url"
} > "$state_dir/deployment.info"
chown "$run_user:$run_group" "$state_dir/deployment.info"
chmod 0600 "$state_dir/deployment.info"

printf 'TASK011_ASD_DEPLOY=PASS\n'
printf 'run_id=%s\n' "$run_id"
printf 'server_unit=%s\n' "$runtime_unit"
printf 'caddy_container=%s\n' "$container_name"
sha256sum "$opt_dir/bin/aisummoner-server"
