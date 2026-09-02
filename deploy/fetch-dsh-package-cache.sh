#!/bin/sh
set -eu

usage() {
  echo "usage: $0 DSH_SOURCE CACHE_DIR" >&2
  exit 2
}

[ "$#" -eq 2 ] || usage

dsh_commit=47f943859bef60e4160492346772ded9b24f765a
node_version=24.19.0
pnpm_version=11.7.0
pnpm_archive_url=https://registry.npmjs.org/pnpm/-/pnpm-11.7.0.tgz
pnpm_archive_sha512=19cc852c120c7125760f2443ee6be0ca5b40f9f50598de1a09a1f177503e010e57c23c77646e01e761de59bf874fb22a3398c33ab9691fc13eb946b6f0f4d620

dsh_source=$(CDPATH= cd -- "$1" && pwd)
mkdir -p "$2"
cache_dir=$(CDPATH= cd -- "$2" && pwd)

[ "$(uname -s)" = Linux ] || { echo "DSH cache fetch supports Linux only" >&2; exit 1; }
[ "$(uname -m)" = x86_64 ] || { echo "DSH cache fetch supports x86_64 only" >&2; exit 1; }
for command_name in curl find git node sha512sum tar; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "$command_name is required" >&2
    exit 1
  }
done
[ "$(node --version)" = "v$node_version" ] || {
  echo "cache fetch requires Node v$node_version" >&2
  exit 1
}
git -C "$dsh_source" rev-parse --verify "$dsh_commit^{commit}" >/dev/null
[ "$(git -C "$dsh_source" rev-parse HEAD)" = "$dsh_commit" ] || {
  echo "DSH source HEAD does not match the pinned commit" >&2
  exit 1
}
git -C "$dsh_source" diff --quiet --ignore-submodules --
git -C "$dsh_source" diff --cached --quiet --ignore-submodules --
[ -z "$(git -C "$dsh_source" ls-files --others --exclude-standard)" ] || {
  echo "DSH source contains untracked files" >&2
  exit 1
}

pnpm_archive=$cache_dir/pnpm-$pnpm_version.tgz
if [ ! -f "$pnpm_archive" ]; then
  curl --fail --location --silent --show-error --retry 2 --connect-timeout 20 \
    --proto '=https' --tlsv1.2 --output "$pnpm_archive" "$pnpm_archive_url"
fi
actual_sha512=$(sha512sum "$pnpm_archive")
actual_sha512=${actual_sha512%% *}
[ "$actual_sha512" = "$pnpm_archive_sha512" ] || {
  echo "pnpm archive failed SHA-512 verification" >&2
  exit 1
}

umask 077
work_dir=$(mktemp -d "$cache_dir/fetch.XXXXXX")
cleanup() {
  if [ -d "$work_dir" ]; then
    find "$work_dir" -depth -delete
  fi
}
trap cleanup EXIT HUP INT TERM

mkdir "$work_dir/pnpm" "$work_dir/home" "$work_dir/npm-cache"
tar -xzf "$pnpm_archive" -C "$work_dir/pnpm"
pnpm_js=$work_dir/pnpm/package/bin/pnpm.cjs
[ -f "$pnpm_js" ] || { echo "pinned pnpm entrypoint is missing" >&2; exit 1; }
[ "$(node "$pnpm_js" --version)" = "$pnpm_version" ] || {
  echo "pinned pnpm version mismatch" >&2
  exit 1
}

pnpm_store=$cache_dir/pnpm-store
mkdir -p "$pnpm_store"
export HOME=$work_dir/home
export PNPM_HOME=$work_dir/pnpm-home
export npm_config_cache=$work_dir/npm-cache
export CI=true
export NO_COLOR=1
export NODE_OPTIONS=--max-old-space-size=2048
(
  cd "$dsh_source"
  node "$pnpm_js" fetch --frozen-lockfile --ignore-scripts --trust-lockfile \
    --child-concurrency=1 --network-concurrency=4 --store-dir "$pnpm_store"
)

cached_file=$(find "$pnpm_store" -type f -print -quit)
[ -n "$cached_file" ] || { echo "DSH pnpm store is empty" >&2; exit 1; }

trap - EXIT HUP INT TERM
cleanup
printf 'Prepared pinned DSH package cache at %s\n' "$cache_dir"
