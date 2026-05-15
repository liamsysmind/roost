// Subscribe to /api/notify/stream via a SharedWorker (so every tab in the
// browser process shares one SSE socket — see notify-worker.js) and
// surface matching events as OS toasts.
//
// Stop-hook gating: when an event carries a `cwd` (i.e. came from a
// `roost notify-stop` invocation), the toast only fires on tabs that are
// *all of*:
//   1. sitting in the same cwd as the event
//   2. hidden / not in the foreground
//   3. currently inside Claude Code (currentApp === 'claude')
//   4. idle for at least `idleAlertAfterMs` (server-supplied, config-driven)
// Events without `cwd` (legacy hook snippets, manual UI POSTs) skip the
// gate entirely so they keep working unchanged.
(() => {
  const hasNotif = 'Notification' in window;

  function ensurePermission() {
    if (!hasNotif) return;
    if (Notification.permission === 'default') {
      try { Notification.requestPermission(); } catch (_) {}
    }
  }
  ensurePermission();
  document.addEventListener('click', ensurePermission, { once: true, passive: true });

  // Pane state piggybacked from fs.js's cwd poll.
  let currentCwd = '';
  let currentApp = '';
  window.addEventListener('roost-cwd-changed', (e) => {
    currentCwd = (e.detail && e.detail.current) || '';
  });
  window.addEventListener('roost-pane-app-changed', (e) => {
    currentApp = (e.detail && e.detail.app) || '';
  });

  // Idle tracking. Any of these resets the timer; we don't track mousemove
  // because passive cursor drift (e.g. external displays) shouldn't count
  // as "the user is actively here".
  let lastActivity = Date.now();
  function bumpActivity() { lastActivity = Date.now(); }
  ['keydown', 'mousedown', 'wheel', 'touchstart'].forEach(ev => {
    document.addEventListener(ev, bumpActivity, { passive: true, capture: true });
  });
  window.addEventListener('focus', bumpActivity);

  function isHidden() {
    return document.visibilityState !== 'visible' || !document.hasFocus();
  }

  // Set from the worker's hello broadcast.
  let idleAlertAfterMs = 30000;

  function show(n) {
    if (!hasNotif || Notification.permission !== 'granted') return;
    try {
      new Notification(n.title || 'roost', {
        body: n.body || '',
        tag: n.session || n.cwd || 'roost',
        icon: '/favicon.ico',
      });
    } catch (e) {
      console.warn('notify failed:', e);
    }
  }

  function shouldAlert(n) {
    // Untargeted events (no cwd) keep the legacy fan-out behavior.
    if (!n.cwd) return true;
    if (n.cwd !== currentCwd) return false;
    if (!isHidden()) return false;
    if (currentApp !== 'claude') return false;
    if (Date.now() - lastActivity < idleAlertAfterMs) return false;
    return true;
  }

  const worker = new SharedWorker('/notify-worker.js');
  worker.port.onmessage = (ev) => {
    const m = ev.data || {};
    if (m.type === 'hello') {
      const h = m.data || {};
      if (typeof h.idle_alert_after_ms === 'number' && h.idle_alert_after_ms > 0) {
        idleAlertAfterMs = h.idle_alert_after_ms;
      }
      return;
    }
    if (m.type === 'notification') {
      try {
        const n = JSON.parse(m.data);
        if (shouldAlert(n)) show(n);
      } catch (_) {}
    }
  };
  worker.port.start();
  // Tell the worker to drop our port on tab close. Without this, dead
  // ports linger in its broadcast set until the last tab leaves.
  window.addEventListener('pagehide', () => {
    try { worker.port.postMessage({ type: 'bye' }); } catch (_) {}
  });
})();
