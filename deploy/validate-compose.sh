#!/bin/sh
set -eu

if [ "$#" -ne 0 ]; then
  echo "validate-compose accepts no arguments; rendered configuration may contain secrets" >&2
  exit 2
fi

environment_file=${AISUMMONER_ENV_FILE:-.env}
if [ "$environment_file" = ".env" ] && [ ! -f "$environment_file" ]; then
  environment_file=.env.example
fi
read_env() {
  key=$1
  [ -f "$environment_file" ] || return 0
  awk -v key="$key" -F= '$1 == key {sub(/^[^=]*=/, ""); sub(/\r$/, ""); print; exit}' "$environment_file"
}

adapter=${AISUMMONER_AGENT_ADAPTER:-$(read_env AISUMMONER_AGENT_ADAPTER)}
adapter=${adapter:-fake}
profiles=${COMPOSE_PROFILES:-$(read_env COMPOSE_PROFILES)}
case ",$profiles," in
  *,opencode,*) opencode_profile=1 ;;
  *) opencode_profile=0 ;;
esac
case "$adapter:$opencode_profile" in
  fake:0) ;;
  deepseek:0) ;;
  opencode:1)
    # MVP deployment always builds this fixed local image from the pinned
    # deploy/OpenCode.Dockerfile; there is no image override path.
    if ! grep -Eq '^[[:space:]]+image:[[:space:]]+aisummoner-opencode:1\.18\.11[[:space:]]*$' deploy/compose.yaml; then
      echo "OpenCode mode requires the fixed local aisummoner-opencode:1.18.11 image" >&2
      exit 1
    fi
    ;;
  fake:1)
    echo "fake adapter must not enable the opencode Compose profile" >&2
    exit 1
    ;;
  deepseek:1)
    echo "deepseek adapter must not enable the opencode Compose profile" >&2
    exit 1
    ;;
  opencode:0)
    echo "opencode adapter requires COMPOSE_PROFILES=opencode" >&2
    exit 1
    ;;
  *)
    echo "AISUMMONER_AGENT_ADAPTER must be fake, deepseek, or opencode" >&2
    exit 1
    ;;
esac

# Never render the interpolated Compose model: it contains runtime credentials.
# --quiet validates and returns only an exit status.
exec docker compose --env-file "$environment_file" -f deploy/compose.yaml config --quiet
