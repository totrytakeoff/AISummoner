#!/bin/sh
set -eu

usage() {
  echo "usage: $0 DSH_SOURCE OUTPUT_TAR_GZ CACHE_DIR" >&2
  exit 2
}

[ "$#" -eq 3 ] || usage

builder_image=node@sha256:934240a162082fd8b8a2f90cd5114446443f1eba1c5378f6687167ca405e6584
dsh_source=$(CDPATH= cd -- "$1" && pwd)
output_argument=$2
cache_dir=$3
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository=$(CDPATH= cd -- "$script_dir/.." && pwd)

output_dir=$(dirname -- "$output_argument")
mkdir -p "$output_dir" "$cache_dir"
output_dir=$(CDPATH= cd -- "$output_dir" && pwd)
cache_dir=$(CDPATH= cd -- "$cache_dir" && pwd)
output_name=$(basename -- "$output_argument")
output=$output_dir/$output_name

[ ! -e "$output" ] || { echo "output already exists" >&2; exit 1; }
[ ! -e "$output.sha256" ] || { echo "output checksum already exists" >&2; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }
docker image inspect "$builder_image" >/dev/null 2>&1 || {
  echo "pinned DSH builder image is missing" >&2
  exit 1
}

for required in \
  node-v24.19.0-linux-x64.tar.xz \
  pnpm-11.7.0.tgz \
  node-addon-landlock-run-linux-x64-0.1.1.tgz; do
  [ -f "$cache_dir/$required" ] || {
    echo "pinned DSH package cache is incomplete" >&2
    exit 1
  }
done
[ -d "$cache_dir/pnpm-store" ] || {
  echo "pinned DSH pnpm store is missing" >&2
  exit 1
}

mkdir -p "$cache_dir/container-home"
chmod 0700 "$cache_dir" "$cache_dir/container-home"

container_name=aisummoner-dsh-builder-$$
cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

docker run --rm --name "$container_name" \
  --cpus=2 --memory=3g --memory-swap=4g --pids-limit=512 \
  --cap-drop=ALL --security-opt no-new-privileges \
  --user "$(id -u):$(id -g)" \
  --volume "$repository:/source/aisummoner:ro" \
  --volume "$dsh_source:/source/dsh:ro" \
  --volume "$output_dir:/output" \
  --volume "$cache_dir:/cache" \
  --workdir /source/aisummoner \
  --env HOME=/cache/container-home \
  --env AISUMMONER_PACKAGE_TMPDIR=/cache \
  --env AISUMMONER_PNPM_ARCHIVE=/cache/pnpm-11.7.0.tgz \
  --env AISUMMONER_PNPM_STORE=/cache/pnpm-store \
  --env AISUMMONER_LANDLOCK_ARCHIVE=/cache/node-addon-landlock-run-linux-x64-0.1.1.tgz \
  "$builder_image" \
  sh deploy/package-dsh-runtime.sh \
    /source/dsh "/output/$output_name" \
    /cache/node-v24.19.0-linux-x64.tar.xz

trap - EXIT HUP INT TERM
printf 'Built portable DSH runtime %s\n' "$output"
