// Subscribe to /api/notify/stream and surface events as OS toasts.
//
// Falls back silently if the browser lacks Notification API or the user
// has denied permission.
(() => {
  const hasNotif = 'Notification' in window;

  function ensurePermission() {
    if (!hasNotif) return;
    if (Notification.permission === 'default') {
      // We can only request permission in response to a user gesture in some
      // browsers. Best-effort attempt at load time.
      try { Notification.requestPermission(); } catch (_) {}
    }
  }
  ensurePermission();
  document.addEventListener('click', ensurePermission, { once: true, passive: true });

  function show(n) {
    if (!hasNotif || Notification.permission !== 'granted') return;
    try {
      new Notification(n.title || 'roost', {
        body: n.body || '',
        tag: n.session || 'roost',
        icon: '/favicon.ico',
      });
    } catch (e) {
      console.warn('notify failed:', e);
    }
  }

  let backoff = 1000;
  function connect() {
    const es = new EventSource('/api/notify/stream');
    es.addEventListener('hello', () => { backoff = 1000; });
    es.onmessage = (ev) => {
      try { show(JSON.parse(ev.data)); } catch (_) {}
    };
    es.onerror = () => {
      es.close();
      setTimeout(connect, backoff);
      backoff = Math.min(backoff * 2, 30000);
    };
  }
  connect();
})();
