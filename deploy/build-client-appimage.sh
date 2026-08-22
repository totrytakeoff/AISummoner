#!/bin/sh
set -eu

usage() {
  echo "usage: $0 CLIENT_BINARY OUTPUT_APPIMAGE [APPIMAGETOOL]" >&2
  exit 2
}

[ "$#" -ge 2 ] && [ "$#" -le 3 ] || usage

client_binary=$1
output_appimage=$2
appimagetool=${3:-appimagetool}

[ -f "$client_binary" ] || {
  echo "client binary does not exist" >&2
  exit 1
}
[ -x "$client_binary" ] || {
  echo "client binary is not executable" >&2
  exit 1
}

case "$appimagetool" in
  */*) [ -x "$appimagetool" ] || {
    echo "appimagetool is not executable" >&2
    exit 1
  } ;;
  *) command -v "$appimagetool" >/dev/null 2>&1 || {
    echo "appimagetool was not found" >&2
    exit 1
  } ;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
output_dir=$(dirname -- "$output_appimage")
mkdir -p "$output_dir"
output_dir=$(CDPATH= cd -- "$output_dir" && pwd)
output_appimage=$output_dir/$(basename -- "$output_appimage")

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/aisummoner-appimage.XXXXXX")
cleanup() {
  rm -rf -- "$work_dir"
}
trap cleanup EXIT HUP INT TERM

app_dir=$work_dir/AISummoner-Client.AppDir
mkdir -p \
  "$app_dir/usr/bin" \
  "$app_dir/usr/share/applications" \
  "$app_dir/usr/share/icons/hicolor/scalable/apps"

install -m 0755 "$client_binary" "$app_dir/usr/bin/aisummoner-client"
install -m 0755 "$script_dir/appimage/AppRun" "$app_dir/AppRun"
install -m 0644 "$script_dir/appimage/aisummoner-client.desktop" \
  "$app_dir/usr/share/applications/aisummoner-client.desktop"
install -m 0644 "$script_dir/appimage/aisummoner-client.svg" \
  "$app_dir/usr/share/icons/hicolor/scalable/apps/aisummoner-client.svg"

ln -s usr/share/applications/aisummoner-client.desktop \
  "$app_dir/aisummoner-client.desktop"
ln -s usr/share/icons/hicolor/scalable/apps/aisummoner-client.svg \
  "$app_dir/aisummoner-client.svg"
ln -s aisummoner-client.svg "$app_dir/.DirIcon"

if [ -n "${APPIMAGE_RUNTIME_FILE:-}" ]; then
  [ -f "$APPIMAGE_RUNTIME_FILE" ] || {
    echo "AppImage runtime file does not exist" >&2
    exit 1
  }
  ARCH=x86_64 "$appimagetool" --no-appstream \
    --runtime-file "$APPIMAGE_RUNTIME_FILE" "$app_dir" "$output_appimage"
else
  ARCH=x86_64 "$appimagetool" --no-appstream "$app_dir" "$output_appimage"
fi
chmod 0755 "$output_appimage"
