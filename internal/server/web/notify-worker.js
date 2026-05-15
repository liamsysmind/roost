// SharedWorker that owns the single /api/notify/stream EventSource on
// behalf of every roost tab in this browser process. Without it each tab
// holds its own SSE socket and we burn through Chrome's 6-per-origin
// HTTP/1.1 connection budget after just a couple of terminal tabs (over
// the SSH tunnel deployment there is no TLS, so h2 is not available to
// raise the cap). See notify.js for the per-tab gating logic that
// consumes our broadcasts.

let ports = [];
// Last hello payload, replayed to newly-connected tabs so they learn
// idle_alert_after_ms without waiting for the next SSE reconnect.
let helloData = null;
let backoff = 1000;

function broadcast(msg) {
  for (const p of ports) {
    try { p.postMessage(msg); } catch (_) {}
  }
}

function connect() {
  const es = new EventSource('/api/notify/stream');
  es.addEventListener('hello', (ev) => {
    backoff = 1000;
    try {
      helloData = JSON.parse(ev.data || '{}');
    } catch (_) {
      helloData = {};
    }
    broadcast({ type: 'hello', data: helloData });
  });
  es.onmessage = (ev) => {
    broadcast({ type: 'notification', data: ev.data });
  };
  es.onerror = () => {
    es.close();
    setTimeout(connect, backoff);
    backoff = Math.min(backoff * 2, 30000);
  };
}

self.onconnect = (e) => {
  const port = e.ports[0];
  ports.push(port);
  if (helloData) port.postMessage({ type: 'hello', data: helloData });
  // SharedWorker has no "tab closed" signal of its own, so the tab tells
  // us on pagehide. Without this, dead ports would accumulate until the
  // last tab leaves and the worker itself dies.
  port.onmessage = (ev) => {
    if (ev.data && ev.data.type === 'bye') {
      ports = ports.filter(p => p !== port);
    }
  };
  port.start();
};

connect();
