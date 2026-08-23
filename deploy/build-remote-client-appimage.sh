#!/bin/sh
set -eu

usage() {
  echo "usage: $0 OUTPUT_APPIMAGE [APPIMAGETOOL] [RUNTIME]" >&2
  exit 2
}

[ "$#" -ge 1 ] && [ "$#" -le 3 ] || usage

output=$1
provided_tool=${2:-}
provided_runtime=${3:-}
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository=$(CDPATH= cd -- "$script_dir/.." && pwd)
output_dir=$(dirname -- "$output")
mkdir -p "$output_dir"
output_dir=$(CDPATH= cd -- "$output_dir" && pwd)
output=$output_dir/$(basename -- "$output")

command -v docker >/dev/null 2>&1 || {
  echo "docker is required" >&2
  exit 1
}

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/aisummoner-remote-appimage.XXXXXX")
cleanup() {
  rm -rf -- "$work_dir"
}
trap cleanup EXIT HUP INT TERM

docker build --file "$script_dir/RemoteClient.Dockerfile" \
  --target remote-client-appdir \
  --output "type=local,dest=$work_dir/AppDir" \
  "$repository"
"$script_dir/check-remote-client-appdir.sh" "$work_dir/AppDir"

fetch_verified() {
  url=$1
  expected=$2
  destination=$3
  curl --fail --location --silent --show-error --retry 2 --connect-timeout 20 \
    --output "$destination" "$url"
  actual=$(sha256sum "$destination" | awk '{print $1}')
  [ "$actual" = "$expected" ] || {
    echo "downloaded packaging tool failed SHA-256 verification" >&2
    exit 1
  }
  chmod 0755 "$destination"
}

verify_file() {
  source_file=$1
  expected=$2
  label=$3
  actual=$(sha256sum "$source_file" | awk '{print $1}')
  [ "$actual" = "$expected" ] || {
    echo "$label failed SHA-256 verification" >&2
    exit 1
  }
}

if [ -n "$provided_tool" ]; then
  [ -x "$provided_tool" ] || { echo "appimagetool is not executable" >&2; exit 1; }
  appimagetool=$provided_tool
  verify_file "$appimagetool" \
    ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0 \
    appimagetool
else
  appimagetool=$work_dir/appimagetool
  fetch_verified \
    https://github.com/AppImage/appimagetool/releases/download/1.9.1/appimagetool-x86_64.AppImage \
    ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0 \
    "$appimagetool"
fi

if [ -n "$provided_runtime" ]; then
  [ -f "$provided_runtime" ] || { echo "AppImage runtime is missing" >&2; exit 1; }
  runtime=$provided_runtime
  verify_file "$runtime" \
    2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d \
    "AppImage runtime"
else
  runtime=$work_dir/runtime-x86_64
  fetch_verified \
    https://github.com/AppImage/type2-runtime/releases/download/20251108/runtime-x86_64 \
    2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d \
    "$runtime"
fi

ARCH=x86_64 APPIMAGE_EXTRACT_AND_RUN=1 "$appimagetool" --no-appstream \
  --runtime-file "$runtime" "$work_dir/AppDir" "$output"
chmod 0755 "$output"
sha256sum "$output" > "$output.sha256"
printf 'Built %s\n' "$output"
