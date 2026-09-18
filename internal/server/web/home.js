// Home page: list existing sessions, create new ones, rename, delete.
(() => {
  const listEl  = document.getElementById('list');
  const emptyEl = document.getElementById('empty');
  const nameEl  = document.getElementById('new-name');
  const newBtn  = document.getElementById('new-btn');

  function fmtSize(b) {
    if (b < 1024) return b + ' B';
    if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB';
    return (b / 1024 / 1024).toFixed(1) + ' MB';
  }

  function fmtAgo(t) {
    const d = new Date(t);
    const s = (Date.now() - d.getTime()) / 1000;
    if (s < 60) return Math.floor(s) + 's ago';
    if (s < 3600) return Math.floor(s / 60) + 'm ago';
    if (s < 86400) return Math.floor(s / 3600) + 'h ago';
    return Math.floor(s / 86400) + 'd ago';
  }

  // Mirrors ValidateID on the server. Everything the server rejects is folded
  // to '-' here rather than sent and bounced: path separators, the '.' and ':'
  // that tmux silently rewrites to '_', whitespace, and control or invisible
  // characters. Letters of every script survive, so a session can be named in
  // the language its owner works in.
  const FORBIDDEN_IN_NAME =
    /[\/\\.:\s\u0000-\u001f\u007f\u200b-\u200f\u202a-\u202e\u2060-\u206f\ufeff]+/gu;

  function sanitizeName(s) {
    return s.trim().replace(FORBIDDEN_IN_NAME, '-').replace(/^-+|-+$/g, '');
  }

  // Session IDs are validated on creation, but the orphan-log branch in the
  // manager derives an ID straight from a filename on disk — so escape before
  // dropping it into innerHTML to keep a crafted log name out of the DOM.
  // Must match app.js and sessions.js exactly: the browser matches tabs by
  // this string, so a disagreement means a second tab instead of a focus.
  function tabNameFor(id) {
    return 'roost-session-' + encodeURIComponent(id);
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c]));
  }

  async function rename(oldID) {
    const raw = prompt(`Rename "${oldID}" to:`, oldID);
    if (raw === null) return;
    const to = sanitizeName(raw);
    if (!to) {
      window.toast && window.toast('That name is empty once / \\ . : and spaces are removed', 'err');
      return;
    }
    if (to === oldID) {
      if (to !== raw.trim()) {
        window.toast && window.toast(`"${raw.trim()}" becomes "${to}", which is the current name`, 'err');
      }
      return;
    }
    const r = await fetch(`/api/sessions/${encodeURIComponent(oldID)}/rename`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'to=' + encodeURIComponent(to),
    });
    if (!r.ok) {
      const msg = 'Rename failed: ' + (await r.text()).trim();
      window.toast ? window.toast(msg, 'err') : alert(msg);
      return;
    }
    load();
  }

  async function del(id) {
    if (!confirm(`Delete session "${id}"? This closes any running shell and removes its log.`)) return;
    const r = await fetch(`/api/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' });
    if (!r.ok) {
      const msg = 'Delete failed: ' + (await r.text()).trim();
      window.toast ? window.toast(msg, 'err') : alert(msg);
      return;
    }
    load();
  }

  async function load() {
    let list = [];
    try {
      const r = await fetch('/api/sessions');
      if (r.ok) list = await r.json();
    } catch (e) {
      console.error('list sessions:', e);
    }
    list.sort((a, b) => new Date(b.last_used) - new Date(a.last_used));
    listEl.innerHTML = '';
    if (list.length === 0) {
      emptyEl.hidden = false;
      return;
    }
    emptyEl.hidden = true;
    for (const s of list) {
      const item = document.createElement('div');
      item.className = 'item';
      item.innerHTML = `
        <a class="link" href="/s/${encodeURIComponent(s.id)}" target="${escapeHtml(tabNameFor(s.id))}">
          <div class="row">
            <span class="id ${s.closed ? 'closed' : ''}">${escapeHtml(s.id)}${s.closed ? ' (closed)' : ''}</span>
            <span class="meta">${s.clients}↔ · ${fmtSize(s.log_size_bytes)} · ${fmtAgo(s.last_used)}</span>
          </div>
        </a>
        <div class="actions">
          <button class="act rename" title="rename">rename</button>
          <button class="act del" title="delete">delete</button>
        </div>`;
      item.querySelector('.rename').addEventListener('click', () => rename(s.id));
      item.querySelector('.del').addEventListener('click', () => del(s.id));
      listEl.appendChild(item);
    }
  }

  function makeID() {
    return crypto.randomUUID
      ? crypto.randomUUID()
      : String(Date.now()) + '-' + Math.random().toString(36).slice(2, 10);
  }

  function go() {
    const name = sanitizeName(nameEl.value);
    const id = name || makeID();
    // No 'noopener': it makes the browser ignore the target name and open a
    // new tab every time, which is the behaviour being removed. The opened
    // page is roost's own, same origin, so there is nothing to withhold.
    window.open('/s/' + encodeURIComponent(id), tabNameFor(id));
    nameEl.value = '';
    // Give the new session a moment to register before refreshing the list.
    setTimeout(load, 400);
  }

  newBtn.addEventListener('click', go);
  nameEl.addEventListener('keydown', (e) => { if (e.key === 'Enter') go(); });

  load();
  setInterval(load, 5000);
})();
