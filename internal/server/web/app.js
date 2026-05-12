// roost terminal frontend.
//
// Connects xterm.js to /ws/terminal. Binary frames carry TTY bytes;
// text frames carry control commands (currently only "resize ROWS COLS").
(() => {
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
  const ws = new WebSocket(`${wsProto}//${location.host}/ws/terminal`);
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
