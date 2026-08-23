#!/bin/sh
set -eu

[ "$#" -eq 1 ] || {
  echo "usage: $0 APPDIR" >&2
  exit 2
}

app_dir=$1
[ -x "$app_dir/AppRun" ]
[ -x "$app_dir/usr/bin/aisummoner-client-ui" ]
[ -x "$app_dir/usr/bin/aisummoner-client" ]
[ -f "$app_dir/usr/bin/qt.conf" ]
[ -f "$app_dir/usr/lib/qt6/plugins/platforms/libqxcb.so" ]
[ -f "$app_dir/usr/share/applications/aisummoner-remote.desktop" ]
[ -f "$app_dir/usr/share/icons/hicolor/scalable/apps/aisummoner-remote.svg" ]

grep -qx 'Exec=aisummoner-client-ui' \
  "$app_dir/usr/share/applications/aisummoner-remote.desktop"
grep -qx 'Terminal=false' \
  "$app_dir/usr/share/applications/aisummoner-remote.desktop"
grep -q 'exec "$app_dir/usr/bin/aisummoner-client-ui" "$@"' "$app_dir/AppRun"
grep -q 'exec "$app_dir/usr/bin/aisummoner-client" "$@"' "$app_dir/AppRun"
if grep -Eq 'eval|--dev|--allow-root-dev' "$app_dir/AppRun"; then
  echo "unsafe AppRun construct" >&2
  exit 1
fi

if LD_LIBRARY_PATH="$app_dir/usr/lib" ldd "$app_dir/usr/bin/aisummoner-client-ui" \
  | grep -q 'not found'; then
  echo "unresolved GUI dependency" >&2
  exit 1
fi
if ldd "$app_dir/usr/bin/aisummoner-client" >/dev/null 2>&1; then
  echo "Go daemon must be statically linked" >&2
  exit 1
fi
if find "$app_dir" -xdev \( -type f -o -type d \) -perm /002 -print | grep -q .; then
  echo "AppDir contains world-writable content" >&2
  exit 1
fi

max_glibc=$(objdump -T "$app_dir/usr/bin/aisummoner-client-ui" \
  | sed -n 's/.*(GLIBC_\([0-9][0-9.]*\)).*/\1/p' \
  | sort -V | tail -n 1)
case "$max_glibc" in
  ''|2.0|2.1|2.2|2.2.*|2.3|2.3.*|2.4|2.5|2.6|2.7|2.8|2.9|\
  2.10|2.11|2.12|2.13|2.14|2.15|2.16|2.17|2.18|2.19|\
  2.20|2.21|2.22|2.23|2.24|2.25|2.26|2.27|2.28|2.29|\
  2.30|2.31|2.32|2.33|2.34|2.35)
    ;;
  *)
    echo "GUI requires glibc newer than Ubuntu 22.04: $max_glibc" >&2
    exit 1
    ;;
esac

printf 'AppDir contract OK (max GLIBC_%s)\n' "${max_glibc:-unknown}"
