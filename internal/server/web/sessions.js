// Sessions tab.
//
// Lives in the right-side tab strip alongside Files and Activity. Polls
// /api/sessions/status every 5 seconds (only while visible) and renders a
// one-row-per-session list with a status dot, agent tag, age, cwd, and
// token usage for agent sessions. Clicking a row navigates the page to
// that session via /s/{id}, which the rest of the app already handles.
(() => {
  const listEl  = document.getElementById('sessions-list');
  const badgeEl = document.getElementById('sessions-tab-badge');
  const paneEl  = document.getElementById('sessions-pane');

  const POLL_MS = 5000;
  let pollTimer = null;
  let inFlight  = false;

  // Mirrors app.js's resolveSessionID so the current row gets highlighted
  // whether the URL uses /s/{id} or /#{id}.
  function currentSessionId() {
    const m = location.pathname.match(/^\/s\/([^\/?#]+)/);
    if (m) return decodeURIComponent(m[1]);
    if (location.hash.length > 1) return decodeURIComponent(location.hash.slice(1));
    return '';
  }

  function fmtAge(seconds) {
    if (seconds < 60) return seconds + 's';
    if (seconds < 3600) return Math.floor(seconds / 60) + 'm';
    if (seconds < 86400) return Math.floor(seconds / 3600) + 'h';
    return Math.floor(seconds / 86400) + 'd';
  }

  function fmtTokens(n) {
    if (!n) return '0';
    if (n < 1000) return String(n);
    if (n < 1_000_000) return (n / 1000).toFixed(1) + 'K';
    return (n / 1_000_000).toFixed(2) + 'M';
  }

  // Show only the last two path segments, prefixed with .../ if more were
  // trimmed. Keeps long paths readable without losing the bit that matters
  // (which subdir the session is in).
  function shortCwd(cwd) {
    if (!cwd) return '';
    const parts = cwd.split('/').filter(Boolean);
    if (parts.length <= 2) return cwd;
    return '.../' + parts.slice(-2).join('/');
  }

  function dotClass(app, closed) {
    if (closed) return 'dot closed';
    if (app === 'claude') return 'dot claude';
    if (app === 'codex')  return 'dot codex';
    return 'dot shell';
  }

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, c => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c]));
  }

  function render(rows) {
    if (!rows || rows.length === 0) {
      listEl.innerHTML = '<li class="empty">no sessions</li>';
      badgeEl.hidden = true;
      return;
    }
    const me = currentSessionId();
    const now = Date.now();
    // Sort: current session first, then by last_used desc.
    rows.sort((a, b) => {
      if (a.id === me) return -1;
      if (b.id === me) return 1;
      return new Date(b.last_used).getTime() - new Date(a.last_used).getTime();
    });

    const html = rows.map(r => {
      const ageSec = Math.max(0, Math.round((now - new Date(r.last_used).getTime()) / 1000));
      const dot   = dotClass(r.app, r.closed);
      const appTag = r.app ? `<span class="app-tag">${escapeHTML(r.app)}</span>` : '';
      const cwd    = r.cwd ? `<span>${escapeHTML(shortCwd(r.cwd))}</span>` : '';
      // Each row3 chunk has a dim label and a bright value, so the eye lands
      // on the numbers instead of the labels.
      const row3Bits = [];
      if (r.model) row3Bits.push(`<span>${escapeHTML(r.model.replace(/^claude-/, ''))}</span>`);
      if (r.context_tokens) row3Bits.push(`<span class="lbl">ctx</span> ${fmtTokens(r.context_tokens)}`);
      if (r.input_tokens || r.output_tokens) {
        row3Bits.push(`<span class="lbl">in</span> ${fmtTokens(r.input_tokens || 0)}`);
        row3Bits.push(`<span class="lbl">out</span> ${fmtTokens(r.output_tokens || 0)}`);
      }
      if (r.clients) row3Bits.push(`<span class="lbl">${r.clients === 1 ? '1 tab' : r.clients + ' tabs'}</span>`);
      const row3 = row3Bits.length ? `<div class="row3">${row3Bits.join('')}</div>` : '';

      return `
        <li class="${r.id === me ? 'current' : ''}" data-id="${escapeHTML(r.id)}">
          <div class="row1">
            <span class="${dot}"></span>
            <span class="sid">${escapeHTML(r.id)}</span>
            ${appTag}
            <span class="age">${fmtAge(ageSec)}</span>
          </div>
          ${cwd ? `<div class="row2">${cwd}</div>` : ''}
          ${row3}
        </li>
      `;
    }).join('');
    listEl.innerHTML = html;

    for (const li of listEl.querySelectorAll('li[data-id]')) {
      li.addEventListener('click', () => {
        const id = li.dataset.id;
        if (id === me) return;
        location.href = '/s/' + encodeURIComponent(id);
      });
    }

    // Badge: count of sessions currently running an agent. Hidden if none,
    // so the tab label stays clean during shell-only days.
    const agentCount = rows.filter(r => !r.closed && (r.app === 'claude' || r.app === 'codex')).length;
    if (agentCount > 0) {
      badgeEl.textContent = String(agentCount);
      badgeEl.hidden = false;
    } else {
      badgeEl.hidden = true;
    }
  }

  async function tick() {
    if (inFlight) return;
    inFlight = true;
    try {
      const r = await fetch('/api/sessions/status');
      if (!r.ok) return;
      const data = await r.json();
      render(data);
    } catch (_) {
      // Network blip. The next tick will retry.
    } finally {
      inFlight = false;
    }
  }

  function startPolling() {
    if (pollTimer !== null) return;
    tick();
    pollTimer = setInterval(tick, POLL_MS);
  }

  function stopPolling() {
    if (pollTimer === null) return;
    clearInterval(pollTimer);
    pollTimer = null;
  }

  // Only poll while the tab is both selected and the page is visible.
  // Saves the round trip when the user is parked on Files or has the
  // browser tab in the background.
  function refreshActiveState() {
    const active = paneEl.classList.contains('active') && !document.hidden;
    if (active) startPolling();
    else stopPolling();
  }

  // ftab buttons toggle .active via ai.js's setTab; observe the pane class
  // for changes rather than coupling to that file's API.
  const observer = new MutationObserver(refreshActiveState);
  observer.observe(paneEl, { attributes: true, attributeFilter: ['class'] });
  document.addEventListener('visibilitychange', refreshActiveState);

  // Cheap one-shot fetch on load so the badge is populated even before
  // the user clicks the tab. Badge updates after that ride on the
  // start/stop polling cycle.
  tick();

  refreshActiveState();
})();
