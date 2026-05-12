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

  const sessionID = resolveSessionID();
  document.title = `roost — ${sessionID.slice(0, 8)}`;

  const tag = document.getElementById('session-tag');
  tag.textContent = sessionID.slice(0, 8);
  tag.addEventListener('click', () => {
    const url = location.origin + '/s/' + encodeURIComponent(sessionID);
    const restore = () => setTimeout(() => tag.textContent = sessionID.slice(0, 8), 800);
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
