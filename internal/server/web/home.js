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

  function sanitizeName(s) {
    return s.trim().replace(/[^A-Za-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '');
  }

  async function rename(oldID) {
    const raw = prompt(`Rename "${oldID}" to:`, oldID);
    if (raw === null) return;
    const to = sanitizeName(raw);
    if (!to || to === oldID) return;
    const r = await fetch(`/api/sessions/${encodeURIComponent(oldID)}/rename`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'to=' + encodeURIComponent(to),
    });
    if (!r.ok) {
      alert('rename failed: ' + (await r.text()).trim());
      return;
    }
    load();
  }

  async function del(id) {
    if (!confirm(`Delete session "${id}"? This closes any running shell and removes its log.`)) return;
    const r = await fetch(`/api/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' });
    if (!r.ok) {
      alert('delete failed: ' + (await r.text()).trim());
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
        <a class="link" href="/s/${encodeURIComponent(s.id)}" target="_blank" rel="noopener">
          <div class="row">
            <span class="id ${s.closed ? 'closed' : ''}">${s.id}${s.closed ? ' (closed)' : ''}</span>
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
    location.href = '/s/' + encodeURIComponent(id);
  }

  newBtn.addEventListener('click', go);
  nameEl.addEventListener('keydown', (e) => { if (e.key === 'Enter') go(); });

  load();
  setInterval(load, 5000);
})();
