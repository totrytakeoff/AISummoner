#!/bin/sh
set -eu

[ "$#" -eq 1 ] || {
  echo "usage: $0 EXTRACTED_RUNTIME_ROOT" >&2
  exit 2
}

root=$(CDPATH= cd -- "$1" && pwd)
[ "$root" != / ] || { echo "refusing filesystem root" >&2; exit 1; }

node=$root/node/bin/node
cli=$root/runtime/lib/bin.js
manifest=$root/runtime-manifest.json
checksums=$root/runtime-files.sha256

[ -x "$node" ] || { echo "bundled Node is missing" >&2; exit 1; }
[ -f "$cli" ] || { echo "DSH CLI is missing" >&2; exit 1; }
[ -f "$manifest" ] || { echo "runtime manifest is missing" >&2; exit 1; }
[ -f "$checksums" ] || { echo "runtime checksums are missing" >&2; exit 1; }

(CDPATH= cd -- "$root" && sha256sum --quiet -c runtime-files.sha256)

"$node" -e '
  const fs = require("node:fs")
  const manifest = JSON.parse(fs.readFileSync(process.argv[1], "utf8"))
  const expected = {
    format: 1,
    platform: "linux-x64",
    dsh_commit: "47f943859bef60e4160492346772ded9b24f765a",
    dsh_version: "0.1.0-rc.5",
    dsh_cli: "runtime/lib/bin.js",
    node_version: "24.19.0",
    node_path: "node/bin/node",
  }
  for (const [key, value] of Object.entries(expected)) {
    if (manifest[key] !== value) throw new Error(`runtime manifest mismatch: ${key}`)
  }
' "$manifest"

[ "$($node --version)" = v24.19.0 ] || { echo "bundled Node version mismatch" >&2; exit 1; }
[ "$($node "$cli" --version)" = 0.1.0-rc.5 ] || { echo "DSH CLI version mismatch" >&2; exit 1; }

runtime_link=$(find "$root/runtime" -type l -print -quit)
[ -z "$runtime_link" ] || { echo "DSH runtime contains a package symlink" >&2; exit 1; }

metadata=$(find "$root/runtime" \( -name .package-lock.json -o -name .modules.yaml -o -name .pnpm-workspace-state-v1.json \) -print -quit)
[ -z "$metadata" ] || { echo "DSH runtime contains build metadata" >&2; exit 1; }

if grep -Eq '"(file|link):/|/tmp/aisummoner-' "$root/runtime/package.json"; then
  echo "DSH runtime manifest contains a build-host path" >&2
  exit 1
fi

landlock_binary=$(find "$root/runtime/node_modules/@deepseek-ai/node-addon-landlock-run-linux-x64/bin" -type f -print -quit)
[ -n "$landlock_binary" ] && [ -x "$landlock_binary" ] || {
  echo "Landlock runtime binary is missing or not executable" >&2
  exit 1
}

"$node" --input-type=module -e '
  import { createRequire } from "node:module"
  const require = createRequire(process.argv[1])
  require("node-pty")
  require("koffi")
  require("sharp")
' "$cli"

printf 'DSH runtime verified: Node 24.19.0, DSH 0.1.0-rc.5\n'
