#!/usr/bin/env bash
# Fetch xterm.js + JS deps into internal/server/web/vendor/.
#
# vendor/ is .gitignore'd, but it must exist before `make build` because the
# server uses //go:embed all:web — the binary serves whatever is on disk at
# build time. Without these files the terminal pane stays blank (file tree
# still works — different code path).
#
# Run once after clone. Re-run to refresh after bumping a pinned version.
set -euo pipefail

VENDOR_DIR="$(cd "$(dirname "$0")/.." && pwd)/internal/server/web/vendor"
mkdir -p "$VENDOR_DIR"

# Pinned versions — bump deliberately. xterm addon versions track the matching
# @xterm/xterm release (5.5.x family here).
fetch() {
  local name="$1" url="$2"
  if ! curl -fsSL --retry 3 --connect-timeout 5 -o "$VENDOR_DIR/$name" "$url"; then
    echo "  FAIL  $name  ($url)" >&2
    return 1
  fi
  printf "  ok    %-22s %8d bytes\n" "$name" "$(wc -c < "$VENDOR_DIR/$name")"
}

echo "Fetching vendor assets into $VENDOR_DIR"
fetch xterm.css          https://cdn.jsdelivr.net/npm/@xterm/xterm@5.5.0/css/xterm.css
fetch xterm.js           https://cdn.jsdelivr.net/npm/@xterm/xterm@5.5.0/lib/xterm.js
fetch addon-fit.js       https://cdn.jsdelivr.net/npm/@xterm/addon-fit@0.10.0/lib/addon-fit.js
fetch addon-webgl.js     https://cdn.jsdelivr.net/npm/@xterm/addon-webgl@0.18.0/lib/addon-webgl.js
fetch addon-search.js    https://cdn.jsdelivr.net/npm/@xterm/addon-search@0.15.0/lib/addon-search.js
fetch marked.min.js      https://cdn.jsdelivr.net/npm/marked@12.0.2/marked.min.js
fetch highlight.min.js   https://cdn.jsdelivr.net/npm/@highlightjs/cdn-assets@11.10.0/highlight.min.js
fetch highlight-dark.css https://cdn.jsdelivr.net/npm/@highlightjs/cdn-assets@11.10.0/styles/github-dark.min.css
echo "Done."
