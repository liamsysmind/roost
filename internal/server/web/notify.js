// Subscribe to /api/notify/stream and surface events as OS toasts.
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

  // Set on hello frame from /api/notify/stream.
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

  let backoff = 1000;
  function connect() {
    const es = new EventSource('/api/notify/stream');
    es.addEventListener('hello', (ev) => {
      backoff = 1000;
      try {
        const h = JSON.parse(ev.data || '{}');
        if (typeof h.idle_alert_after_ms === 'number' && h.idle_alert_after_ms > 0) {
          idleAlertAfterMs = h.idle_alert_after_ms;
        }
      } catch (_) {}
    });
    es.onmessage = (ev) => {
      try {
        const n = JSON.parse(ev.data);
        if (shouldAlert(n)) show(n);
      } catch (_) {}
    };
    es.onerror = () => {
      es.close();
      setTimeout(connect, backoff);
      backoff = Math.min(backoff * 2, 30000);
    };
  }
  connect();
})();
