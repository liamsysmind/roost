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
    theme: { background: '#000000', foreground: '#e6e6e6' },
    allowProposedApi: true,
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

  // Test affordance: call __roostDropWS() from the browser console to
  // simulate a TCP-level transport drop. Server stays up, cookie stays
  // valid — exactly the real-world case (WiFi blip, laptop sleep) we
  // care about. The auto-reconnect path should kick in and recover.
  window.__roostDropWS = () => { if (ws) ws.close(); };
})();
