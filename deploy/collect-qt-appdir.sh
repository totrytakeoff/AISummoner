#!/bin/bash
set -euo pipefail

if [[ $# -ne 1 || ! -d $1/usr/bin ]]; then
  echo "usage: collect-qt-appdir APPDIR" >&2
  exit 2
fi

app_dir=$(readlink -f "$1")
library_dir=$app_dir/usr/lib
plugin_destination=$library_dir/qt6/plugins
if command -v qtpaths6 >/dev/null 2>&1; then
  qtpaths_command=$(command -v qtpaths6)
elif [[ -x /usr/lib/qt6/bin/qtpaths6 ]]; then
  # Ubuntu 22.04 installs the real Qt 6 tool outside PATH.  Its /usr/bin/qtpaths
  # is a qtchooser wrapper and fails unless an unrelated default is configured.
  qtpaths_command=/usr/lib/qt6/bin/qtpaths6
elif command -v qmake6 >/dev/null 2>&1; then
  qtpaths_command=
else
  echo "Qt paths tool was not found" >&2
  exit 1
fi
if [[ -n $qtpaths_command ]]; then
  plugin_source=$("$qtpaths_command" --plugin-dir)
else
  plugin_source=$(qmake6 -query QT_INSTALL_PLUGINS)
fi

install -d -m 0755 "$library_dir" "$plugin_destination"
for category in platforms platforminputcontexts xcbglintegrations imageformats iconengines; do
  if [[ -d $plugin_source/$category ]]; then
    install -d -m 0755 "$plugin_destination/$category"
    find "$plugin_source/$category" -maxdepth 1 -type f -name '*.so' -print0 \
      | while IFS= read -r -d '' plugin; do
          install -m 0644 "$plugin" "$plugin_destination/$category/$(basename "$plugin")"
        done
  fi
done

is_system_library() {
  case "$1" in
    ld-linux-x86-64.so.2|libc.so.6|libdl.so.2|libm.so.6|libpthread.so.0|\
    libresolv.so.2|librt.so.1|libutil.so.1|libnss_*.so.2|libGL.so.*|libEGL.so.*|\
    libGLX.so.*|libOpenGL.so.*|libdrm.so.*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

changed=1
while [[ $changed -eq 1 ]]; do
  changed=0
  while IFS= read -r -d '' candidate; do
    if ! file -b "$candidate" | grep -q '^ELF '; then
      continue
    fi
    while IFS= read -r dependency; do
      [[ -n $dependency && -f $dependency ]] || continue
      base=$(basename "$dependency")
      if is_system_library "$base" || [[ -e $library_dir/$base ]]; then
        continue
      fi
      install -m 0644 -D "$dependency" "$library_dir/$base"
      changed=1
    done < <(ldd "$candidate" 2>/dev/null \
      | sed -n -e 's/.*=> \(\/[^ ]*\).*/\1/p' -e 's/^[[:space:]]*\(\/[^ ]*\).*/\1/p')
  done < <(find "$app_dir/usr/bin" "$library_dir" -type f -print0)
done

while IFS= read -r -d '' executable; do
  if patchelf --print-rpath "$executable" >/dev/null 2>&1; then
    patchelf --set-rpath '$ORIGIN/../lib' "$executable"
  fi
done < <(find "$app_dir/usr/bin" -maxdepth 1 -type f -print0)

while IFS= read -r -d '' library; do
  if patchelf --print-rpath "$library" >/dev/null 2>&1; then
    case "$library" in
      */qt6/plugins/*) patchelf --set-rpath '$ORIGIN/../../..' "$library" ;;
      *) patchelf --set-rpath '$ORIGIN' "$library" ;;
    esac
  fi
done < <(find "$library_dir" -type f -print0)

if ! find "$plugin_destination/platforms" -maxdepth 1 -name 'libqxcb.so' -type f | grep -q .; then
  echo "Qt xcb platform plugin was not bundled" >&2
  exit 1
fi

if LD_LIBRARY_PATH="$library_dir" ldd "$app_dir/usr/bin/aisummoner-client-ui" | grep -q 'not found'; then
  echo "GUI has unresolved shared-library dependencies" >&2
  exit 1
fi
