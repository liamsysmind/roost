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
    fontFamily: 'ui-monospace, "JetBrains Mono", "SF Mono", Menlo, Consolas, monospace',
    fontSize: 14,
    scrollback: 10000,
    theme: { background: '#000000', foreground: '#e6e6e6' },
    allowProposedApi: true,
  });

  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(document.getElementById('term'));

  try {
    term.loadAddon(new WebglAddon.WebglAddon());
  } catch (e) {
    console.warn('WebGL renderer unavailable, using DOM:', e);
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
          if (t && ws.readyState === WebSocket.OPEN) ws.send(encoder.encode(t));
        }).catch(() => {});
      }
      return false;
    }

    return true;
  });

  const wsProto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsURL = `${wsProto}//${location.host}/ws/terminal/${encodeURIComponent(sessionID)}`;
  const ws = new WebSocket(wsURL);
  ws.binaryType = 'arraybuffer';

  ws.onopen = () => {
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
    term.writeln(`\r\n\x1b[31m[roost] disconnected${ev.reason ? ': ' + ev.reason : ''}\x1b[0m`);
  };

  ws.onerror = (e) => console.error('ws error', e);

  const encoder = new TextEncoder();
  term.onData((data) => {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(encoder.encode(data));
    }
  });

  function sendResize() {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(`resize ${term.rows} ${term.cols}`);
    }
  }

  let resizeTimer = null;
  window.addEventListener('resize', () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      fit.fit();
      sendResize();
    }, 80);
  });
})();
