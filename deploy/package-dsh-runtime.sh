#!/bin/sh
set -eu

usage() {
  echo "usage: $0 DSH_SOURCE OUTPUT_TAR_GZ [NODE_ARCHIVE]" >&2
  exit 2
}

[ "$#" -ge 2 ] && [ "$#" -le 3 ] || usage

dsh_source=$1
output_argument=$2
provided_node_archive=${3:-}

dsh_commit=47f943859bef60e4160492346772ded9b24f765a
dsh_version=0.1.0-rc.5
node_version=24.19.0
node_archive_name=node-v24.19.0-linux-x64.tar.xz
node_archive_url=https://nodejs.org/download/release/v24.19.0/node-v24.19.0-linux-x64.tar.xz
node_archive_sha256=14b342e71204f811bde6153be8e04b62aef63c236fef92b55f9c83154b409647
pnpm_version=11.7.0
pnpm_archive_url=https://registry.npmjs.org/pnpm/-/pnpm-11.7.0.tgz
pnpm_archive_sha512=19cc852c120c7125760f2443ee6be0ca5b40f9f50598de1a09a1f177503e010e57c23c77646e01e761de59bf874fb22a3398c33ab9691fc13eb946b6f0f4d620
landlock_version=0.1.1
landlock_archive_url=https://registry.npmjs.org/@deepseek-ai/node-addon-landlock-run-linux-x64/-/node-addon-landlock-run-linux-x64-0.1.1.tgz
landlock_archive_sha512=3870333d6d82a1efe26286c0240000f02795ac1a0a578067347b20c2f5f039f8ac871914552592bddcb1ad93e2c18457bada48195ad2a3dcd5dd6390cb67ae04

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository=$(CDPATH= cd -- "$script_dir/.." && pwd)
dsh_source=$(CDPATH= cd -- "$dsh_source" && pwd)

output_dir=$(dirname -- "$output_argument")
mkdir -p "$output_dir"
output_dir=$(CDPATH= cd -- "$output_dir" && pwd)
output=$output_dir/$(basename -- "$output_argument")

[ ! -e "$output" ] || { echo "output already exists" >&2; exit 1; }
[ ! -e "$output.sha256" ] || { echo "output checksum already exists" >&2; exit 1; }
[ "$(uname -s)" = Linux ] || { echo "DSH runtime packaging supports Linux only" >&2; exit 1; }
[ "$(uname -m)" = x86_64 ] || { echo "DSH runtime packaging supports x86_64 only" >&2; exit 1; }

for command_name in awk curl find git gzip sha256sum sha512sum sort tar xargs; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "$command_name is required" >&2
    exit 1
  }
done

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

require_memory() {
  phase=$1
  available_kb=$(awk '/^MemAvailable:/ {print $2}' /proc/meminfo)
  [ -n "$available_kb" ] && [ "$available_kb" -ge 4194304 ] || {
    echo "$phase requires at least 4 GiB MemAvailable" >&2
    exit 1
  }
  printf '%s: MemAvailable=%s kB\n' "$phase" "$available_kb"
}

package_tmp=${AISUMMONER_PACKAGE_TMPDIR:-$output_dir}
available_blocks=$(df -Pk "$package_tmp" | awk 'NR == 2 {print $4}')
[ -n "$available_blocks" ] && [ "$available_blocks" -ge 6291456 ] || {
  echo "DSH runtime packaging requires at least 6 GiB free temporary space" >&2
  exit 1
}

umask 077
work_dir=$(mktemp -d "$package_tmp/aisummoner-dsh-package.XXXXXX")
finished=0
cleanup() {
  if [ "$finished" -ne 1 ]; then
    [ ! -e "$output" ] || rm -f -- "$output"
    [ ! -e "$output.sha256" ] || rm -f -- "$output.sha256"
  fi
  if [ -d "$work_dir" ]; then
    find "$work_dir" -depth -delete
  fi
}
trap cleanup EXIT HUP INT TERM

verify_sha256() {
  file=$1
  expected=$2
  label=$3
  actual=$(sha256sum "$file" | awk '{print $1}')
  [ "$actual" = "$expected" ] || {
    echo "$label failed SHA-256 verification" >&2
    exit 1
  }
}

verify_sha512() {
  file=$1
  expected=$2
  label=$3
  actual=$(sha512sum "$file" | awk '{print $1}')
  [ "$actual" = "$expected" ] || {
    echo "$label failed SHA-512 verification" >&2
    exit 1
  }
}

download() {
  url=$1
  destination=$2
  curl --fail --location --silent --show-error --retry 2 --connect-timeout 20 \
    --proto '=https' --tlsv1.2 --output "$destination" "$url"
}

require_memory source
source_tree=$work_dir/source
mkdir "$source_tree"
git -C "$dsh_source" archive --format=tar --output="$work_dir/source.tar" "$dsh_commit"
tar -xf "$work_dir/source.tar" -C "$source_tree"

if [ -n "$provided_node_archive" ]; then
  node_archive_dir=$(dirname -- "$provided_node_archive")
  node_archive_dir=$(CDPATH= cd -- "$node_archive_dir" && pwd)
  node_archive=$node_archive_dir/$(basename -- "$provided_node_archive")
  [ -f "$node_archive" ] || { echo "provided Node archive is missing" >&2; exit 1; }
else
  node_archive=$work_dir/$node_archive_name
  download "$node_archive_url" "$node_archive"
fi
verify_sha256 "$node_archive" "$node_archive_sha256" "Node archive"

bundle=$work_dir/aisummoner-dsh-runtime
mkdir "$bundle"
tar -xJf "$node_archive" -C "$bundle"
mv "$bundle/node-v$node_version-linux-x64" "$bundle/node"
node=$bundle/node/bin/node
[ -x "$node" ] || { echo "bundled Node executable is missing" >&2; exit 1; }
[ "$($node --version)" = "v$node_version" ] || { echo "bundled Node version mismatch" >&2; exit 1; }

pnpm_archive=${AISUMMONER_PNPM_ARCHIVE:-$work_dir/pnpm-$pnpm_version.tgz}
if [ ! -f "$pnpm_archive" ]; then
  download "$pnpm_archive_url" "$pnpm_archive"
fi
verify_sha512 "$pnpm_archive" "$pnpm_archive_sha512" "pnpm archive"
mkdir "$work_dir/pnpm"
tar -xzf "$pnpm_archive" -C "$work_dir/pnpm"
pnpm_js=$work_dir/pnpm/package/bin/pnpm.cjs
[ -f "$pnpm_js" ] || { echo "pinned pnpm entrypoint is missing" >&2; exit 1; }
pnpm_store=${AISUMMONER_PNPM_STORE:-$work_dir/pnpm-store}
mkdir "$work_dir/bin" "$work_dir/build-home" "$work_dir/npm-cache"
mkdir -p "$pnpm_store"
pnpm=$work_dir/bin/pnpm
printf '#!/bin/sh\nexec "%s" "%s" "$@"\n' "$node" "$pnpm_js" > "$pnpm"
chmod 0700 "$pnpm"
[ "$($pnpm --version)" = "$pnpm_version" ] || { echo "pinned pnpm version mismatch" >&2; exit 1; }

build_path=$work_dir/bin:$bundle/node/bin:/usr/bin:/bin
export PATH=$build_path
export HOME=$work_dir/build-home
export PNPM_HOME=$work_dir/pnpm-home
export npm_config_cache=$work_dir/npm-cache
# The verified binary archive already carries the exact headers for its ABI.
# Keep node-gyp offline instead of letting it download a second mutable copy.
export npm_config_nodedir=$bundle/node
export CI=true
export NO_COLOR=1
export NODE_OPTIONS=--max-old-space-size=2048

require_memory install
(
  cd "$source_tree"
  "$pnpm" install --frozen-lockfile --offline --trust-lockfile \
    --child-concurrency=1 --network-concurrency=4 \
    --store-dir "$pnpm_store"
)

# A shared pnpm store can restore a host-specific side-effect artifact, while
# `pnpm rebuild` may skip this transitive package. Delete only node-pty's build
# directory in the fresh source tree and invoke the node-gyp bundled with the
# pinned pnpm archive directly, using the verified Node headers above.
node_pty_package=$(find "$source_tree/node_modules/.pnpm" \
  -path '*/node_modules/node-pty/package.json' -type f -print -quit)
[ -n "$node_pty_package" ] || {
  echo "node-pty package is missing from the pinned install" >&2
  exit 1
}
node_pty_package=$(dirname -- "$node_pty_package")
node_gyp=$work_dir/pnpm/package/dist/node_modules/node-gyp/bin/node-gyp.js
[ -f "$node_gyp" ] || { echo "pinned node-gyp entrypoint is missing" >&2; exit 1; }
if [ -d "$node_pty_package/build" ]; then
  find "$node_pty_package/build" -depth -delete
fi
require_memory native
(
  cd "$node_pty_package"
  "$node" "$node_gyp" rebuild
)
node_pty_binary=$node_pty_package/build/Release/pty.node
[ -f "$node_pty_binary" ] || node_pty_binary=
[ -n "$node_pty_binary" ] || {
  echo "node-pty portable build output is missing" >&2
  exit 1
}

require_memory build
(
  cd "$source_tree"
  "$pnpm" run release:verify --family dsh
  "$pnpm" run release:verify --family vendor
  "$pnpm" run build
)

require_memory deploy
(
  cd "$source_tree"
  "$pnpm" --filter @deepseek-ai/dsh --prod deploy --offline --ignore-scripts --trust-lockfile \
    --store-dir "$pnpm_store" \
    --config.inject-workspace-packages=true \
    --config.node-linker=hoisted \
    --config.link-workspace-packages=true \
    "$bundle/runtime"
)

"$node" "$script_dir/materialize-dsh-runtime.mjs" \
  "$bundle/runtime" "$source_tree/apps/cli/node_modules" \
  "$source_tree/python/sdk-runtime/package.json" \
  "$source_tree/python/sdk-runtime/node_modules" \
  "$source_tree/node_modules/.pnpm/node_modules"

# pnpm's hoisted deploy can replace a materialized native package with the
# pristine content-addressed copy. Publish the artifact built above only after
# the final dependency closure is in place, then prove it loads below.
node_pty_destination=$bundle/runtime/node_modules/node-pty/build/Release/pty.node
mkdir -p "$(dirname -- "$node_pty_destination")"
install -m 0755 "$node_pty_binary" "$node_pty_destination"

find "$bundle/runtime" -name .package-lock.json -type f -delete
find "$bundle/runtime" -name .modules.yaml -type f -delete
find "$bundle/runtime" -name .pnpm-workspace-state-v1.json -type f -delete

remaining_link=$(find "$bundle/runtime" -type l -print -quit)
[ -z "$remaining_link" ] || { echo "runtime contains an external package link" >&2; exit 1; }

landlock_archive=${AISUMMONER_LANDLOCK_ARCHIVE:-$work_dir/landlock-$landlock_version.tgz}
if [ ! -f "$landlock_archive" ]; then
  download "$landlock_archive_url" "$landlock_archive"
fi
verify_sha512 "$landlock_archive" "$landlock_archive_sha512" "Landlock archive"
landlock_extract=$work_dir/landlock
mkdir "$landlock_extract"
tar -xzf "$landlock_archive" -C "$landlock_extract"
landlock_destination=$bundle/runtime/node_modules/@deepseek-ai/node-addon-landlock-run-linux-x64
if [ -e "$landlock_destination" ]; then
  find "$landlock_destination" -depth -delete
fi
mkdir -p "$(dirname -- "$landlock_destination")"
mv "$landlock_extract/package" "$landlock_destination"
landlock_binary=$(find "$landlock_destination/bin" -type f -print -quit)
[ -n "$landlock_binary" ] && [ -x "$landlock_binary" ] || {
  echo "Landlock runtime binary is missing or not executable" >&2
  exit 1
}

cli=$bundle/runtime/lib/bin.js
[ -f "$cli" ] || { echo "deployed DSH CLI is missing" >&2; exit 1; }
[ "$($node "$cli" --version)" = "$dsh_version" ] || { echo "deployed DSH version mismatch" >&2; exit 1; }

smoke=$bundle/runtime/aisummoner-runtime-smoke.mjs
printf '%s\n' \
  "await import('node-pty')" \
  "await import('koffi')" \
  "await import('sharp')" > "$smoke"
(
  cd "$bundle/runtime"
  "$node" "$smoke"
)
rm -f "$smoke"

if grep -Eq '"(file|link):/|/tmp/aisummoner-' "$bundle/runtime/package.json"; then
  echo "runtime manifest contains a build-host path" >&2
  exit 1
fi

cp "$repository/THIRD_PARTY_NOTICES.md" "$bundle/THIRD_PARTY_NOTICES-AISUMMONER.md"
cp "$source_tree/THIRD_PARTY_NOTICES.md" "$bundle/THIRD_PARTY_NOTICES-DSH.md"
cp "$source_tree/LICENSE" "$bundle/DSH-LICENSE"

cat > "$bundle/runtime-manifest.json" <<EOF
{
  "format": 1,
  "platform": "linux-x64",
  "dsh_commit": "$dsh_commit",
  "dsh_version": "$dsh_version",
  "dsh_cli": "runtime/lib/bin.js",
  "node_version": "$node_version",
  "node_path": "node/bin/node",
  "node_archive_sha256": "$node_archive_sha256",
  "pnpm_version": "$pnpm_version",
  "pnpm_archive_sha512": "$pnpm_archive_sha512",
  "landlock_version": "$landlock_version",
  "landlock_archive_sha512": "$landlock_archive_sha512"
}
EOF

(
  cd "$bundle"
  find . -type f ! -name runtime-files.sha256 -print0 \
    | LC_ALL=C sort -z \
    | xargs -0 sha256sum > runtime-files.sha256
)

source_date_epoch=$(git -C "$dsh_source" show -s --format=%ct "$dsh_commit")
require_memory archive
(
  cd "$work_dir"
  tar --sort=name --mtime="@$source_date_epoch" --owner=0 --group=0 --numeric-owner \
    -cf - aisummoner-dsh-runtime | gzip -n -9 > "$output"
)
(CDPATH= cd -- "$output_dir" && sha256sum "$(basename -- "$output")") > "$output.sha256"
chmod 0644 "$output" "$output.sha256"
finished=1

printf 'Built %s\n' "$output"
