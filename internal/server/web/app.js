// roost terminal frontend.
//
// Each browser tab connects to its own named session, identified by URL:
//   /s/{id}    explicit path-based session id (bookmarkable, shareable)
//   /#{id}     fragment fallback (handy for tab clones)
//   /          generates a fresh UUID, rewrites URL to /#{uuid}
(() => {
  function resolveSessionID() {
    const m = location.pathname.match(/^\/s\/([^\/?#]+)/);
    if (m) return decodeURIComponent(m[1]);
    if (location.hash.length > 1) return decodeURIComponent(location.hash.slice(1));
    const id = (crypto.randomUUID
      ? crypto.randomUUID()
      : String(Date.now()) + '-' + Math.random().toString(36).slice(2, 10));
    history.replaceState(null, '', '#' + id);
    return id;
  }

  let sessionID = resolveSessionID();

  // A session opened without a name carries a generated id, in one of two
  // shapes: crypto.randomUUID() where it exists, and the timestamp+random
  // fallback in resolveSessionID where it doesn't — plain http on a LAN
  // address is not a secure context, so randomUUID is absent exactly there.
  // Neither shape names anything.
  const GENERATED_ID_RE = /^([0-9a-f]{8}-[0-9a-f]{4}-|\d{13}-[a-z0-9]+$)/;

  // Generated ids show only their leading 8 chars; a name the user chose
  // shows in full.
  function displayID(id) {
    return GENERATED_ID_RE.test(id) ? id.slice(0, 8) : id;
  }

  function refreshSessionUI() {
    // The tab strip is where a name actually pays off: with a dozen roost
    // tabs open a browser shows only the first few characters of each, so the
    // name alone beats "roost — name". A generated id names no project, so
    // fall back to the app name rather than spend the tab on hex digits.
    document.title = GENERATED_ID_RE.test(sessionID) ? 'roost' : sessionID;
    tag.textContent = displayID(sessionID);
  }

  const tag = document.getElementById('session-tag');
  refreshSessionUI();

  tag.addEventListener('click', () => {
    const url = location.origin + '/s/' + encodeURIComponent(sessionID);
    const restore = () => setTimeout(refreshSessionUI, 800);
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(url).then(
        () => { tag.textContent = 'copied!'; restore(); },
        () => { tag.textContent = 'copy failed'; restore(); },
      );
    } else {
      tag.textContent = url;
      restore();
    }
  });

  // Rename — keeps the WebSocket alive because the server just re-keys
  // the existing *Session struct, no reconnect required. We just need to
  // update the browser's URL and our local sessionID.
  function sanitizeName(s) {
    return s.trim().replace(/[^A-Za-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '');
  }

  const renameBtn = document.getElementById('session-rename');
  renameBtn.addEventListener('click', async () => {
    const raw = prompt(`Rename session\nCurrent: ${sessionID}\nNew:`, sessionID);
    if (raw === null) return;
    const to = sanitizeName(raw);
    if (!to || to === sessionID) return;
    try {
      const r = await fetch(`/api/sessions/${encodeURIComponent(sessionID)}/rename`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: 'to=' + encodeURIComponent(to),
      });
      if (!r.ok) {
        const msg = 'Rename failed: ' + (await r.text()).trim();
        window.toast ? window.toast(msg, 'err') : alert(msg);
        return;
      }
      sessionID = to;
      refreshSessionUI();
      history.replaceState(null, '', '/s/' + encodeURIComponent(to));
      window.toast && window.toast(`Renamed to ${to}`, 'ok');
    } catch (e) {
      const msg = 'Rename failed: ' + e.message;
      window.toast ? window.toast(msg, 'err') : alert(msg);
    }
  });

  const term = new Terminal({
    cursorBlink: true,
    fontFamily: 'ui-monospace, "JetBrains Mono", "SF Mono", Menlo, Consolas, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans Mono CJK SC", "Noto Sans CJK SC", "WenQuanYi Micro Hei Mono", monospace',
    fontSize: 14,
    scrollback: 100000,
    theme: {
      background: '#000000',
      foreground: '#e7eae8',
      cursor: '#54b487',
      cursorAccent: '#000000',
      selectionBackground: 'rgba(84,180,135,0.30)',
    },
    allowProposedApi: true,
    // When a TUI (claude/codex/vim/htop) turns on mouse tracking, xterm.js
    // disables its own text selection and forwards drags to the app — so you
    // can't drag-select multiple lines to copy. Other platforms escape this by
    // holding Shift while dragging; on Mac the force-selection modifier is
    // Option, but only if this flag is on (it defaults to false, which leaves
    // Mac with no escape hatch at all). Enable it so Option+drag selects even
    // inside a mouse-tracking app.
    macOptionClickForcesSelection: true,
  });

  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(document.getElementById('term'));
  window.roostTerm = term;

  // WebGL renderer is intentionally disabled. It pre-rasterizes glyphs from a
  // single resolved font into a GPU atlas and cannot fall back per-character,
  // so any CJK / emoji char absent from the primary monospace font renders as
  // the atlas's missing-glyph placeholder (a row of dashes). The DOM renderer
  // uses native browser font fallback, honouring the full fontFamily stack.

  // Search addon — exposed on window so the AI panel can jump to prompts.
  try {
    const search = new SearchAddon.SearchAddon();
    term.loadAddon(search);
    window.roostSearchAddon = search;
  } catch (e) {
    console.warn('Search addon failed to load:', e);
  }

  fit.fit();

  // Ctrl+C / Ctrl+Shift+C / Ctrl+Shift+V handling.
  //
  // Standard terminal-in-browser ergonomics: by default xterm.js sends every
  // Ctrl+C straight to the PTY as SIGINT, which loses the OS's "copy" muscle
  // memory entirely. Distinguish by selection state.
  term.attachCustomKeyEventHandler((e) => {
    if (e.type !== 'keydown') return true;

    // Ctrl+C with selection → copy, do NOT forward as SIGINT.
    if (e.ctrlKey && !e.shiftKey && (e.key === 'c' || e.key === 'C') && term.hasSelection()) {
      const sel = term.getSelection();
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(sel).catch(() => {});
      }
      term.clearSelection();
      return false;
    }

    // Ctrl+Shift+C → always copy whatever's selected.
    if (e.ctrlKey && e.shiftKey && (e.key === 'C' || e.key === 'c')) {
      if (term.hasSelection() && navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(term.getSelection()).catch(() => {});
      }
      return false;
    }

    // Ctrl+Shift+V → paste clipboard into the PTY.
    if (e.ctrlKey && e.shiftKey && (e.key === 'V' || e.key === 'v')) {
      if (navigator.clipboard && navigator.clipboard.readText) {
        navigator.clipboard.readText().then((t) => {
          if (t && ws && ws.readyState === WebSocket.OPEN) ws.send(encoder.encode(t));
        }).catch(() => {});
      }
      return false;
    }

    // Shift+PageUp/PageDown/Home/End → scroll xterm.js viewport rather than
    // sending the key sequence to the PTY. Works even when a TUI app has
    // mouse tracking on and would otherwise swallow wheel events.
    if (e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey) {
      if (e.key === 'PageUp')   { term.scrollPages(-1); return false; }
      if (e.key === 'PageDown') { term.scrollPages(1);  return false; }
      if (e.key === 'Home')     { term.scrollToTop();    return false; }
      if (e.key === 'End')      { term.scrollToBottom(); return false; }
    }

    return true;
  });

  // Wheel scrolling: always scroll the xterm.js scrollback buffer, regardless
  // of whether a TUI app (Codex, Claude Code, etc.) has enabled mouse tracking.
  //
  // xterm.js's own wheel listener is only attached when an app has enabled
  // mouse-tracking modes, so attachCustomWheelEventHandler alone doesn't cover
  // the common case (plain bash). And xterm.js's "native" viewport scroll
  // (overflow-y: scroll on .xterm-viewport) doesn't reliably pick up wheel
  // events when the WebGL canvas is overlaid on top.
  //
  // Solution: attach our own wheel listener on the #term container in the
  // capture phase. This catches wheel events before anything else, scrolls
  // the buffer via the Terminal API, and prevents default so the viewport
  // doesn't double-scroll. Shift+wheel passes through (for `less`/`man`).
  const termEl = document.getElementById('term');
  termEl.addEventListener('wheel', (e) => {
    if (e.shiftKey) return;
    let lines;
    if (e.deltaMode === 1) {           // DOM_DELTA_LINE
      lines = e.deltaY;
    } else if (e.deltaMode === 2) {    // DOM_DELTA_PAGE
      term.scrollPages(Math.sign(e.deltaY));
      e.preventDefault();
      return;
    } else {                            // DOM_DELTA_PIXEL
      lines = e.deltaY / 24;
    }
    const n = lines > 0 ? Math.max(1, Math.ceil(lines)) : Math.min(-1, Math.floor(lines));
    term.scrollLines(n);
    e.preventDefault();
  }, { passive: false, capture: true });

  // Copy-on-select: when a drag (or double/triple-click) finishes with text
  // selected, copy it to the clipboard automatically. This is the primary copy
  // path so no shortcut is needed — which matters on Windows/Chrome, where
  // Ctrl+Shift+C is the browser's DevTools shortcut and can't be intercepted
  // by the page. Ctrl+C on a live selection still copies too. mouseup is a user
  // gesture, so the async clipboard write is permitted.
  termEl.addEventListener('mouseup', () => {
    const sel = term.getSelection();
    if (sel && sel.trim() && navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(sel).catch(() => {});
    }
  });

  // --- clickable file paths in terminal output ---
  // xterm.js's link provider runs per visible buffer line. We match path-like
  // tokens and dispatch a custom event to fs.js, which owns the preview modal.
  // The cross-IIFE handoff via window event avoids exposing internal modal
  // functions on the global object.
  //
  // Regex matches three shapes, optionally followed by :LINE or :LINE:COL:
  //   /abs/path/file.ext
  //   ./rel/file.ext or ../rel/file.ext or ~/rel/file.ext
  //   bare/relative/file.ext (requires at least one '/' to dodge false
  //                            positives like version strings)
  // Bare filenames with no '/' are skipped intentionally; agent output is
  // noisy enough that whitelisting "looks like a real path" is the safer
  // default. The lookbehind blocks matches inside URLs (chars before would
  // be ':' or '/') and inside longer paths.
  const FILE_PATH_RE = /(?<![\w./:])((?:\.{1,2}|~)?\/[\w.~-]+(?:\/[\w.~-]+)*|[\w.~-]+(?:\/[\w.~-]+)+)(?::(\d+)(?::(\d+))?)?/g;

  // A relative path is only meaningful against the cwd that was in effect when
  // the line was PRINTED, which is not necessarily the cwd now. Resolving every
  // click against one live value silently opens the wrong file as soon as the
  // shell cd's, or when one agent exits and another starts in a different
  // project — the whole earlier scrollback keeps pointing at the new directory.
  // So record a mark each time the polled cwd changes, and resolve a click
  // against the last mark at or above the clicked line.
  //
  // Marks hold xterm.js markers rather than raw line numbers because the buffer
  // trims from the top once `scrollback` is exceeded: a marker follows its line
  // as everything shifts up, and disposes itself once its line is gone.
  //
  // fs.js polls every 2s, so a line printed in the window between an actual
  // `cd` and the next poll is attributed to the previous cwd. Narrowing that
  // would mean shell integration, which the cwd design deliberately avoids.
  const cwdMarks = [];    // { marker, cwd }, ascending by line
  let baseCwd = '';       // cwd governing the buffer above the first live mark
  let lastTerminalCwd = '';

  // Trimming only ever removes leading marks, so promoting a disposed mark's
  // cwd to baseCwd keeps the lines it governed — which may still be on screen —
  // anchored instead of silently falling back to the current directory.
  function pruneCwdMarks() {
    while (cwdMarks.length && cwdMarks[0].marker.isDisposed) {
      baseCwd = cwdMarks.shift().cwd;
    }
  }

  window.addEventListener('roost-cwd-changed', (e) => {
    const cwd = (e.detail && e.detail.current) || '';
    lastTerminalCwd = cwd;
    if (!cwd) return;
    pruneCwdMarks();
    const marker = term.registerMarker(0);
    if (marker) cwdMarks.push({ marker, cwd });
  });

  // Lines replayed from the disk log predate every mark (the page has only
  // been open since the last load), so they fall back to the current cwd —
  // exactly the old behaviour, no regression.
  function cwdForLine(line) {
    pruneCwdMarks();
    let cwd = baseCwd;
    for (const m of cwdMarks) {
      if (m.marker.line > line) break;
      cwd = m.cwd;
    }
    return cwd || lastTerminalCwd;
  }

  // Walk a buffer line's cells once and build a JS-char-index to 1-based
  // xterm column lookup. Wide chars (CJK, emoji) occupy two cells but one
  // char in translateToString output, so without this map the link
  // decoration drifts left by the count of preceding wide chars and ends
  // early. The trailing sentinel covers "position immediately past the
  // last char" for computing the inclusive end column.
  function buildColMap(line) {
    const map = [];
    let col = 1;
    for (let i = 0; i < line.length; i++) {
      const cell = line.getCell(i);
      if (!cell) break;
      const w = cell.getWidth();
      if (w === 0) continue;            // spacer following a wide char
      const text = cell.getChars();
      const repeat = text.length || 1;  // empty cells render as ' '
      for (let j = 0; j < repeat; j++) map.push(col);
      col += w;
    }
    map.push(col);
    return map;
  }

  // A long line occupies several buffer rows, and provideLinks is called once
  // per row — so anything that wrapped was matched only as far as the row
  // break. A URL split across three rows gave one truncated link on the first
  // row and nothing at all on the other two, which is why tapping the middle
  // of a wrapped URL did nothing. Phones hit this constantly: a narrow
  // terminal wraps almost any real URL.
  //
  // xterm's isWrapped flag cannot answer this on its own. tmux never lets the
  // terminal auto-wrap; it repositions the cursor for every row it paints
  // (ESC[20;2H …), so each row of a wrapped line arrives as an independent
  // line with isWrapped false. The flag is still trusted when it IS set, but
  // the load-bearing signal is geometric: a row whose content runs into the
  // terminal's last column was cut there rather than having ended there.
  //
  // Continuation rows keep the emitter's own indent — Claude Code indents its
  // output by two columns — so their leading blanks are dropped before the
  // join. A URL cannot contain a space, so nothing real is lost, and `skip`
  // keeps the char-index-to-column mapping exact.
  //
  // The heuristic can be wrong: two unrelated rows that both happen to fill
  // the pane exactly will be joined, and a match spanning that seam points
  // somewhere wrong. That is the failure mode a truncated wrapped URL already
  // had, so it trades a guaranteed wrong answer for an occasional one.
  const MAX_WRAP_ROWS = 64;

  // Content reaches the last column. colMap is used rather than string length
  // because a wide char (CJK, emoji) is one char but two columns.
  function fillsLastColumn(line, colMap) {
    const trimmed = line.translateToString(false).replace(/\s+$/, '').length;
    if (!trimmed) return false;
    return (colMap[trimmed] || 0) - 1 >= term.cols;
  }

  function logicalLine(lineNumber) {
    const buf = term.buffer.active;
    const at = (y) => {
      const l = buf.getLine(y);
      if (!l) return null;
      const colMap = buildColMap(l);
      return { l, colMap, fills: fillsLastColumn(l, colMap) };
    };
    // Row y continues row y-1.
    const continues = (prev, cur) => !!prev && (prev.fills || (!!cur && cur.l.isWrapped));

    let startY = lineNumber - 1;
    for (let n = 0; n < MAX_WRAP_ROWS && startY > 0; n++) {
      if (!continues(at(startY - 1), at(startY))) break;
      startY--;
    }

    const rows = [];
    let text = '';
    for (let n = 0; n < MAX_WRAP_ROWS; n++) {
      const y = startY + n;
      const cur = at(y);
      if (!cur) break;
      if (n > 0 && !continues(at(y - 1), cur)) break;
      let t = cur.l.translateToString(false);
      let skip = 0;
      if (n > 0) {
        skip = t.length - t.replace(/^\s+/, '').length;
        t = t.slice(skip);
      }
      rows.push({ y: y + 1, text: t, colMap: cur.colMap, skip, offset: text.length });
      text += t;
    }
    return { rows, text };
  }

  function rowAt(rows, idx) {
    for (const r of rows) {
      if (idx >= r.offset && idx < r.offset + r.text.length) return r;
    }
    return rows.length ? rows[rows.length - 1] : null;
  }

  // Map a [start, end) span of the joined text to an xterm range. start.y and
  // end.y may differ — xterm.js supports a link spanning rows, which is what
  // makes every row of a wrapped URL clickable.
  function spanToRange(rows, startIdx, endIdx) {
    const first = rowAt(rows, startIdx);
    const last  = rowAt(rows, endIdx - 1);
    if (!first || !last) return null;
    const sOff = startIdx - first.offset + first.skip;
    const eOff = endIdx - last.offset + last.skip;
    return {
      start: { x: first.colMap[sOff] || (sOff + 1), y: first.y },
      end:   { x: (last.colMap[eOff] || (eOff + 1)) - 1, y: last.y },  // inclusive
    };
  }

  // The regex only says "this token is path-shaped", which is not the same as
  // "this file is here": agents quote hypothetical paths, paths in other
  // repos, and paths that existed three commits ago. Decorating those produces
  // links that look live and die on click. POST the candidates to
  // /api/fs/exist, which resolves each against the line's cwd, enforces
  // containment under the fs root, follows symlinks and stats the result —
  // then decorate only the survivors, carrying the server's resolved `rel` so
  // the click needs no second resolution.
  //
  // Keyed on line text + cwd: a redrawn line re-validates on its new text, and
  // identical text under a different cwd doesn't reuse a stale verdict. The
  // cache matters because xterm calls provideLinks on every newly-hovered row.
  const existCache = new Map();
  const EXIST_CACHE_MAX = 400;
  const EXIST_MAX_PATHS = 64;   // server rejects >256; a real line has a handful

  const existPending = new Set();

  function existKey(cwd, paths) {
    return cwd + '\u0000' + paths.join('\u0000');
  }

  // Fetches in the background and never returns anything: provideLinks must
  // answer synchronously (see the provider below), so validation can only ever
  // apply to a verdict that is already cached. Warming is keyed on cwd+paths
  // rather than on the row, so the same path token seen anywhere else on
  // screen is already answered.
  function warmExistCache(cwd, paths) {
    const key = existKey(cwd, paths);
    if (existCache.has(key) || existPending.has(key)) return;
    existPending.add(key);
    fetch('/api/fs/exist', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ cwd, paths }),
    })
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (!data) return;
        const found = new Map();
        for (const res of (data.results || [])) {
          if (res && res.exists && res.rel) found.set(res.input, res.rel);
        }
        // Crude but adequate: the cache collapses repeat lookups, it is not
        // meant to be long-lived, so drop it wholesale rather than track LRU.
        if (existCache.size >= EXIST_CACHE_MAX) existCache.clear();
        existCache.set(key, found);
      })
      .catch(() => {})
      .finally(() => existPending.delete(key));
  }

  // provideLinks MUST call back synchronously. xterm.js will not adopt a later
  // provider's link while an earlier one is still outstanding, so awaiting the
  // validation request here delayed every link on the row — this one and the
  // URL provider below — by a round trip. On a desktop the hover precedes the
  // click by long enough to hide it; on a phone Safari synthesises mousemove,
  // mousedown and mouseup inside a single tap, so the link was still
  // unresolved when mouseup ran and the tap did nothing at all.
  //
  // So answer from the cache if a verdict is already there, otherwise answer
  // with every candidate — the behaviour from before validation existed — and
  // warm the cache for next time.
  term.registerLinkProvider({
    provideLinks(lineNumber, callback) {
      const { rows, text } = logicalLine(lineNumber);
      if (!text || text.indexOf('/') < 0) return callback(undefined);
      const cands = [];
      FILE_PATH_RE.lastIndex = 0;
      let m;
      while ((m = FILE_PATH_RE.exec(text)) !== null && cands.length < EXIST_MAX_PATHS) {
        cands.push({
          path:     m[1],
          lineHint: m[2] ? parseInt(m[2], 10) : null,
          colHint:  m[3] ? parseInt(m[3], 10) : null,
          startIdx: m.index,
          endIdx:   m.index + m[0].length,
          whole:    m[0],
        });
      }
      if (!cands.length) return callback(undefined);
      const cwd = cwdForLine(lineNumber - 1);
      const paths = cands.map(c => c.path);
      const found = existCache.get(existKey(cwd, paths)) || null;
      if (!found) warmExistCache(cwd, paths);

      const links = [];
      for (const c of cands) {
        if (found && !found.has(c.path)) continue;
        const range = spanToRange(rows, c.startIdx, c.endIdx);
        if (!range) continue;
        const rel = found ? found.get(c.path) : undefined;
        links.push({
          text: c.whole,
          range,
          decorations: { pointerCursor: true, underline: true },
          activate: () => {
            window.dispatchEvent(new CustomEvent('roost-open-preview', {
              detail: { path: c.path, line: c.lineHint, col: c.colHint, cwd, rel },
            }));
          },
        });
      }
      callback(links.length ? links : undefined);
    },
  });

  // --- clickable http(s) URLs → open in a new tab ---
  // A second link provider (xterm queries all of them). Kept separate from the
  // file-path provider so each regex stays simple; the file-path lookbehind
  // already refuses to match inside URLs, so the two never fight over a range.
  // The excluded set carries CJK and full-width punctuation on purpose. A
  // line like "部署好了 https://x.app/a.html（重新整理）" is ordinary Chinese
  // prose, but U+FF08 is not whitespace, so a plain \S+ match swallows the
  // whole parenthetical and the click 404s on a percent-encoded tail. CJK
  // *ideographs* stay allowed — https://zh.wikipedia.org/wiki/臺灣 is a valid
  // IRI and must remain clickable; it is only the punctuation that can never
  // appear inside a URL.
  const URL_RE =
    /\bhttps?:\/\/[^\s<>"'`\u3000-\u303f\uff01-\uff0f\uff1a-\uff20\uff3b-\uff40\uff5b-\uff65]+/g;
  term.registerLinkProvider({
    provideLinks(lineNumber, callback) {
      const { rows, text } = logicalLine(lineNumber);
      if (!text || text.indexOf('://') < 0) return callback(undefined);
      const links = [];
      URL_RE.lastIndex = 0;
      let m;
      while ((m = URL_RE.exec(text)) !== null) {
        // Trim trailing sentence punctuation so "see https://x.com." or
        // "(https://x.com)" don't drag the period / paren into the URL.
        const url = m[0].replace(/[.,;:!?)\]}'"]+$/, '');
        if (!url) continue;
        const range = spanToRange(rows, m.index, m.index + url.length);
        if (!range) continue;
        links.push({
          text: url,
          range,
          decorations: { pointerCursor: true, underline: true },
          // noopener/noreferrer: the opened page gets no handle back to roost.
          activate: () => { window.open(url, '_blank', 'noopener,noreferrer'); },
        });
      }
      callback(links.length ? links : undefined);
    },
  });

  const wsProto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsURL = `${wsProto}//${location.host}/ws/terminal/${encodeURIComponent(sessionID)}`;

  // Auto-reconnect: transport-level WS drops are inevitable on long-lived
  // connections (laptop sleep, WiFi roam, SSH tunnel reconnect, Chrome
  // freezing background tabs). The tmux backend survives them, so all we
  // need is to redial. Backoff caps at 30s and we keep trying indefinitely —
  // the user's machine waking up an hour later should still recover.
  // The exception is code 1000 with a reason: that's the server saying the
  // shell exited, so redialing would just spawn a fresh shell out of context.
  const BACKOFF_MS = [500, 1000, 2000, 4000, 8000, 16000, 30000];
  let ws = null;
  let reconnectAttempt = 0;
  let reconnectTimer = null;
  let gaveUp = false;

  function connect() {
    reconnectTimer = null;
    ws = new WebSocket(wsURL);
    ws.binaryType = 'arraybuffer';

    ws.onopen = () => {
      if (reconnectAttempt > 0) {
        term.writeln(`\r\n\x1b[32m[roost] reconnected\x1b[0m`);
      }
      reconnectAttempt = 0;
      sendResize();
      term.focus();
    };

    ws.onmessage = (ev) => {
      if (typeof ev.data === 'string') {
        term.writeln(`\r\n\x1b[33m[roost] ${ev.data}\x1b[0m`);
        return;
      }
      term.write(new Uint8Array(ev.data));
    };

    ws.onclose = (ev) => {
      // Shell-exited is terminal — reconnecting would silently spawn a fresh
      // shell with no history, which is more confusing than a clear message.
      if (ev.code === 1000 && ev.reason) {
        term.writeln(`\r\n\x1b[31m[roost] disconnected: ${ev.reason}\x1b[0m`);
        gaveUp = true;
        return;
      }
      if (reconnectAttempt === 0) {
        term.writeln(`\r\n\x1b[33m[roost] disconnected, reconnecting…\x1b[0m`);
      }
      scheduleReconnect();
    };

    ws.onerror = (e) => console.error('ws error', e);
  }

  function scheduleReconnect() {
    if (gaveUp || reconnectTimer !== null) return;
    const delay = BACKOFF_MS[Math.min(reconnectAttempt, BACKOFF_MS.length - 1)];
    reconnectAttempt++;
    reconnectTimer = setTimeout(connect, delay);
  }

  // When the OS reports the network came back, short-circuit whatever backoff
  // we're sitting on and try immediately. Avoids waiting 30s after a long sleep.
  window.addEventListener('online', () => {
    if (gaveUp || ws === null || ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) return;
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    connect();
  });

  const encoder = new TextEncoder();
  term.onData((data) => {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(encoder.encode(data));
    }
  });

  function sendResize() {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(`resize ${term.rows} ${term.cols}`);
    }
  }

  connect();

  let resizeTimer = null;
  window.addEventListener('resize', () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      fit.fit();
      sendResize();
    }, 80);
  });

  function sendPty(data) {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(encoder.encode(data));
  }

  // Mobile input bar — see index.html. Composing a whole command in a real
  // <input> sidesteps two xterm-on-mobile problems: the soft keyboard covering
  // the prompt, and iOS dictation duplicating characters through xterm's hidden
  // textarea. Submit sends the text plus Enter; the key chips send raw control
  // sequences the on-screen keyboard can't produce.
  const composeEl    = document.getElementById('compose');
  const composeInput = document.getElementById('compose-input');
  const KEYSEQ = {
    esc: '\x1b', tab: '\t', 'ctrl-c': '\x03', enter: '\r',
    up: '\x1b[A', down: '\x1b[B', left: '\x1b[D', right: '\x1b[C',
  };
  // Reassigned once Web Speech is wired up below; a no-op until then. Submit
  // calls it so a still-running recognizer can't repopulate the cleared field.
  let resetVoiceInput = () => {};
  if (composeEl) {
    composeEl.addEventListener('submit', (e) => {
      e.preventDefault();
      sendPty(composeInput.value + '\r');
      composeInput.value = '';
      resetVoiceInput();
      composeInput.focus(); // keep the keyboard up for the next command
    });
    composeEl.querySelectorAll('.ckey').forEach((b) => {
      b.addEventListener('click', () => {
        const seq = KEYSEQ[b.dataset.key];
        if (seq) sendPty(seq);
        composeInput.focus();
      });
    });
  }

  // A pasted image/file was just uploaded by the file panel (fs.js). Drop its
  // absolute path into the prompt so the agent reads the file directly instead
  // of shelling out to `find ~ -iname <name>` to locate a bare filename — which
  // hangs for minutes on a large / iCloud-synced home directory. Mobile shows
  // the compose bar, so fill that; desktop has none, so type at the PTY prompt.
  window.addEventListener('roost-paste-path', (e) => {
    const paths = (e.detail && e.detail.paths) || [];
    if (!paths.length) return;
    const text = paths.join(' ') + ' ';
    const composeShown = composeEl && getComputedStyle(composeEl).display !== 'none';
    if (composeShown && composeInput) {
      const base = composeInput.value.replace(/\s*$/, '');
      composeInput.value = (base ? base + ' ' : '') + text;
      composeInput.focus();
    } else {
      sendPty(text);
    }
  });

  // Voice input via the Web Speech API rather than the keyboard's dictation
  // key. iOS Safari's built-in dictation has an OS-level bug that re-inserts
  // the previous phrase into a web <input> on every committed chunk (the URL
  // bar is the lone exception). We can't disable that key, but we can offer an
  // alternative that bypasses it entirely: we receive recognition results in JS
  // and write the field ourselves, so nothing doubles. Hidden when the browser
  // has no Speech API (e.g. desktop Firefox) — typing still works everywhere.
  const micBtn = document.getElementById('compose-mic');
  const SR = window.SpeechRecognition || window.webkitSpeechRecognition;
  if (micBtn && SR) {
    micBtn.hidden = false;
    const recog = new SR();
    recog.continuous = true;
    recog.interimResults = true;
    recog.lang = navigator.language || 'en-US';
    let recognizing = false, srBase = '', srFinal = '';
    recog.onresult = (e) => {
      // A final result can arrive after the user hit send and we tore dictation
      // down; without this guard it would rewrite the just-cleared field.
      if (!recognizing) return;
      let interim = '';
      for (let i = e.resultIndex; i < e.results.length; i++) {
        const tr = e.results[i][0].transcript;
        if (e.results[i].isFinal) srFinal += tr; else interim += tr;
      }
      composeInput.value = srBase + srFinal + interim;
    };
    const stop = () => { recognizing = false; micBtn.classList.remove('rec'); };
    recog.onend = stop;
    recog.onerror = stop;
    // Sending the composed line clears the input; stop any in-flight dictation
    // and drop its accumulators so a trailing result can't refill it.
    resetVoiceInput = () => {
      srBase = ''; srFinal = '';
      if (recognizing) { recognizing = false; micBtn.classList.remove('rec'); try { recog.stop(); } catch (_) {} }
    };
    micBtn.addEventListener('click', () => {
      if (recognizing) { recog.stop(); return; }
      srBase = composeInput.value ? composeInput.value.replace(/\s*$/, '') + ' ' : '';
      srFinal = '';
      try {
        recog.start();
        recognizing = true;
        micBtn.classList.add('rec');
        composeInput.focus();
      } catch (_) { stop(); }
    });
  }

  // Keep the terminal sized to the visible area, above both the on-screen
  // keyboard and the mobile input bar. The layout viewport stays full-height
  // when the keyboard opens, but visualViewport shrinks — so without this the
  // prompt hides behind the keyboard. We lift #stage by (keyboard overlap +
  // compose-bar height) and float the compose bar just above the keyboard.
  const stageEl = document.getElementById('stage');
  const vv = window.visualViewport;
  let vvTimer = null;
  let lastOverlap = -1;
  // Reposition the compose bar / terminal for the visible area — but only on a
  // real keyboard open/close, never on the sub-pixel jitter iOS emits while
  // dictating. Each DOM write near the focused field during dictation corrupts
  // its range tracking and makes it re-insert the previous phrase (the URL bar,
  // which has no surrounding layout churn, never doubles — same reason). So we
  // listen to `resize` only (not the noisy `scroll`) and freeze once the
  // keyboard is up: a delta under 24px is treated as jitter and ignored.
  function layoutForViewport(force) {
    const overlap = vv ? Math.max(0, window.innerHeight - vv.height - vv.offsetTop) : 0;
    if (!force && Math.abs(overlap - lastOverlap) < 24) return;
    lastOverlap = overlap;
    const composeH = composeEl ? composeEl.offsetHeight : 0; // 0 when hidden (desktop)
    if (composeEl) composeEl.style.bottom = overlap + 'px';
    stageEl.style.bottom = (overlap + composeH) + 'px';
    clearTimeout(vvTimer);
    vvTimer = setTimeout(() => {
      fit.fit();
      sendResize();
      term.scrollToBottom();
    }, 80);
  }
  if (vv) vv.addEventListener('resize', () => layoutForViewport(false));
  window.addEventListener('orientationchange', () => setTimeout(() => layoutForViewport(true), 250));
  layoutForViewport(true); // initial: positions the compose bar / insets the stage

  // Test affordance: call __roostDropWS() from the browser console to
  // simulate a TCP-level transport drop. Server stays up, cookie stays
  // valid — exactly the real-world case (WiFi blip, laptop sleep) we
  // care about. The auto-reconnect path should kick in and recover.
  window.__roostDropWS = () => { if (ws) ws.close(); };
})();
