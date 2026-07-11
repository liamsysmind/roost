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

  // UUIDs show only the leading 8-char block; named sessions show full.
  function displayID(id) {
    return /^[0-9a-f]{8}-[0-9a-f]{4}-/.test(id) ? id.slice(0, 8) : id;
  }

  function refreshSessionUI() {
    document.title = `roost — ${displayID(sessionID)}`;
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

  // Cache the terminal session's cwd; fs.js dispatches this on every change.
  // Required for resolving relative paths at click time.
  let lastTerminalCwd = '';
  window.addEventListener('roost-cwd-changed', (e) => {
    lastTerminalCwd = (e.detail && e.detail.current) || '';
  });

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

  term.registerLinkProvider({
    provideLinks(lineNumber, callback) {
      const line = term.buffer.active.getLine(lineNumber - 1);
      if (!line) return callback(undefined);
      const text = line.translateToString(true);
      if (!text || text.indexOf('/') < 0) return callback(undefined);
      const colMap = buildColMap(line);
      const links = [];
      FILE_PATH_RE.lastIndex = 0;
      let m;
      while ((m = FILE_PATH_RE.exec(text)) !== null) {
        const path = m[1];
        const lineHint = m[2] ? parseInt(m[2], 10) : null;
        const colHint  = m[3] ? parseInt(m[3], 10) : null;
        const startIdx = m.index;
        const endIdx   = m.index + m[0].length;
        const startCol = colMap[startIdx] || (startIdx + 1);
        const endCol   = (colMap[endIdx] || (endIdx + 1)) - 1;  // inclusive
        links.push({
          text: m[0],
          range: {
            start: { x: startCol, y: lineNumber },
            end:   { x: endCol,   y: lineNumber },
          },
          decorations: { pointerCursor: true, underline: true },
          activate: () => {
            window.dispatchEvent(new CustomEvent('roost-open-preview', {
              detail: { path, line: lineHint, col: colHint, cwd: lastTerminalCwd },
            }));
          },
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
