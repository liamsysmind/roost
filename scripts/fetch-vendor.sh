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

# Patch xterm.js 5.5.0's CompositionHelper.
#
# _finalizeComposition(false) — the path CompositionHelper.keydown takes when
# any keydown that isn't the IME's own (keyCode 229, or a bare 16/17/18)
# arrives mid-composition — sends the pre-edit buffer but never records it in
# _dataAlreadySent. A macOS input-source switch (Caps Lock, Ctrl+Space,
# Ctrl+Option+Space, …) is exactly such a keydown *and* force-commits the IME,
# so the compositionend that follows runs _finalizeComposition(true), whose
# `start += _dataAlreadySent.length` offset subtracts nothing, and the same
# text goes to the PTY twice. Typing Chinese and switching to English left a
# duplicate of the whole uncommitted run.
#
# Recording what the false path sent makes the existing offset dedupe work,
# which covers every switch key rather than a hand-listed few and keeps
# Ctrl+Space usable as NUL. Patching here rather than editing vendor/ directly
# because vendor/ is .gitignore'd — a direct edit vanishes on a fresh clone
# with no error, and CJK silently starts duplicating again.
#
# Anchors on the minified else-branch. If a version bump moves it the build
# fails rather than silently shipping the bug; re-derive the anchor then.
XTERM_JS="$VENDOR_DIR/xterm.js" node <<'PATCH_EOF'
const fs = require('fs');
const file = process.env.XTERM_JS;
let src = fs.readFileSync(file, 'utf8');

// Each entry replaces `find` with `repl` exactly once. A miss aborts the
// fetch (set -e) rather than silently shipping unpatched xterm.js — the only
// symptom of a silent miss is CJK duplicating again, weeks later.
const patches = [
  {
    name: 'finalizeComposition records what it sent',
    find:
      'this._isSendingComposition=!1;' +
      'const e=this._textarea.value.substring(' +
      'this._compositionPosition.start,this._compositionPosition.end);' +
      'this._coreService.triggerDataEvent(e,!0)',
    repl:
      'this._isSendingComposition=!1;' +
      'const e=this._textarea.value.substring(' +
      'this._compositionPosition.start,this._compositionPosition.end);' +
      'this._dataAlreadySent=e;this._coreService.triggerDataEvent(e,!0)',
  },
  {
    // Every keyCode-229 keydown schedules its own setTimeout(0) with its own
    // captured `before`. Type fast enough that the queue backs up and several
    // land after _isComposing goes false; each then sends its own delta from a
    // stale baseline, so overlapping suffixes reach the PTY. Worse, `before`
    // is "" for the first one and "x".replace("","") returns "x" unchanged —
    // that callback re-sends the entire run. Coalesce to a single pending
    // callback (earliest baseline wins, so the delta still covers everything)
    // and strip the baseline as a prefix instead of as a substring.
    name: 'coalesce textarea-change callbacks',
    find:
      '_handleAnyTextareaChanges(){const e=this._textarea.value;setTimeout((()=>{' +
      'if(!this._isComposing){const t=this._textarea.value,i=t.replace(e,"");',
    repl:
      '_handleAnyTextareaChanges(){if(this._roostPendingChange)return;' +
      'this._roostPendingChange=!0;const e=this._textarea.value;setTimeout((()=>{' +
      'this._roostPendingChange=!1;' +
      'if(!this._isComposing){const t=this._textarea.value,' +
      'i=t.startsWith(e)?t.slice(e.length):t.replace(e,"");',
  },
];

for (const p of patches) {
  const hits = src.split(p.find).length - 1;
  if (hits !== 1) {
    console.error(`  FAIL  xterm.js patch "${p.name}": anchor matched ${hits}x (want 1)`);
    console.error('        xterm.js changed shape — re-derive the anchor before shipping.');
    process.exit(1);
  }
  src = src.replace(p.find, p.repl);
}
fs.writeFileSync(file, src);
console.log(`  ok    xterm.js           ${patches.length} composition patches applied`);
PATCH_EOF
fetch addon-fit.js       https://cdn.jsdelivr.net/npm/@xterm/addon-fit@0.10.0/lib/addon-fit.js
fetch addon-webgl.js     https://cdn.jsdelivr.net/npm/@xterm/addon-webgl@0.18.0/lib/addon-webgl.js
fetch addon-search.js    https://cdn.jsdelivr.net/npm/@xterm/addon-search@0.15.0/lib/addon-search.js
fetch marked.min.js      https://cdn.jsdelivr.net/npm/marked@12.0.2/marked.min.js
fetch highlight.min.js   https://cdn.jsdelivr.net/npm/@highlightjs/cdn-assets@11.10.0/highlight.min.js
fetch highlight-dark.css https://cdn.jsdelivr.net/npm/@highlightjs/cdn-assets@11.10.0/styles/github-dark.min.css
echo "Done."
