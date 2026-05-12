// File tree panel for roost.
//
// Tree state: each open directory tracks its expanded set so opening a folder
// then refreshing doesn't collapse everything. Children are lazy-loaded.
(() => {
  const treeEl   = document.getElementById('ftree');
  const crumbEl  = document.getElementById('fcrumb');
  const statusEl = document.getElementById('fstatus');
  const fsEl     = document.getElementById('fs');

  // Root directory for the panel (replaced by /api/fs/root on init).
  let rootAbs = '/';
  // Current "anchor" dir we're showing. We always display from this point;
  // the breadcrumb jumps further down by replacing the anchor.
  let cwd = '';
  // Set of fully-qualified paths currently expanded.
  const expanded = new Set();

  function setStatus(msg, err) {
    statusEl.textContent = msg;
    statusEl.classList.toggle('err', !!err);
  }

  function absCwd() {
    const r = rootAbs.replace(/\/+$/, '');
    return cwd ? r + '/' + cwd : r;
  }

  function setIdleStatus(extra) {
    setStatus('drag files here → ' + absCwd() + (extra ? ' · ' + extra : ''));
  }

  function fmtSize(b) {
    if (b < 1024) return b + 'B';
    if (b < 1024 * 1024) return (b / 1024).toFixed(1) + 'K';
    if (b < 1024 * 1024 * 1024) return (b / 1024 / 1024).toFixed(1) + 'M';
    return (b / 1024 / 1024 / 1024).toFixed(1) + 'G';
  }

  async function api(method, path, opts = {}) {
    const r = await fetch(path, { method, ...opts });
    if (!r.ok) {
      const t = await r.text();
      throw new Error(t.trim() || r.statusText);
    }
    if (r.status === 204) return null;
    return r.json();
  }

  async function listDir(rel) {
    const r = await api('GET', '/api/fs/list?path=' + encodeURIComponent(rel || ''));
    return r.entries;
  }

  function renderCrumb() {
    const parts = cwd.split('/').filter(Boolean);
    const frags = [];
    frags.push(`<span data-path="">${escapeHTML(rootAbs)}</span>`);
    let acc = '';
    for (const p of parts) {
      acc += '/' + p;
      frags.push(' / ' + `<span data-path="${escapeHTML(acc)}">${escapeHTML(p)}</span>`);
    }
    crumbEl.innerHTML = frags.join('');
    for (const s of crumbEl.querySelectorAll('span')) {
      s.addEventListener('click', () => {
        cwd = s.dataset.path;
        expanded.clear();
        refresh();
      });
    }
  }

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, c => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c]));
  }

  function nodeHTML(entry, depth) {
    const id = entry.path;
    const cls = entry.is_dir ? 'dir' : 'file';
    const chev = entry.is_dir
      ? (expanded.has(id) ? '▾' : '▸')
      : '';
    const icon = entry.is_dir ? '📁' : '·';
    const sizeOrCount = entry.is_dir ? '' : fmtSize(entry.size);
    return `
      <div class="node ${cls} indent-${Math.min(depth, 6)}" data-path="${escapeHTML(id)}" data-is-dir="${entry.is_dir}">
        <span class="chev">${chev}</span>
        <span class="icon">${icon}</span>
        <span class="name">${escapeHTML(entry.name)}</span>
        <span class="actions">
          ${entry.is_dir
            ? ''
            : `<button class="act dl"  title="download">↓</button>`}
          <button class="act rn"  title="rename">⋯</button>
          <button class="act del" title="delete">✕</button>
        </span>
        <span style="color:#555;font-size:0.65rem;padding-right:0.3rem">${sizeOrCount}</span>
      </div>
      ${entry.is_dir ? `<div class="children ${expanded.has(id) ? 'open' : ''}" data-children-of="${escapeHTML(id)}"></div>` : ''}
    `;
  }

  async function refresh() {
    renderCrumb();
    try {
      const entries = await listDir(cwd);
      treeEl.innerHTML = entries.map(e => nodeHTML(e, 1)).join('');
      bindNodes(treeEl);
      // Re-expand previously open subtrees
      for (const p of [...expanded]) {
        const holder = treeEl.querySelector(`[data-children-of="${cssEscape(p)}"]`);
        if (holder) await loadChildren(holder, depthOf(p));
      }
      setIdleStatus(`${entries.length} item${entries.length === 1 ? '' : 's'}`);
    } catch (e) {
      setStatus(e.message, true);
    }
  }

  function cssEscape(s) {
    if (CSS && CSS.escape) return CSS.escape(s);
    return s.replace(/(["\\])/g, '\\$1');
  }

  function depthOf(p) {
    return p.split('/').filter(Boolean).length;
  }

  function bindNodes(container) {
    for (const n of container.querySelectorAll(':scope > .node')) {
      const path = n.dataset.path;
      const isDir = n.dataset.isDir === 'true';

      n.querySelector('.chev').addEventListener('click', async (e) => {
        e.stopPropagation();
        if (!isDir) return;
        await toggleExpand(n, path);
      });
      n.querySelector('.name').addEventListener('click', async () => {
        if (isDir) {
          await toggleExpand(n, path);
        } else {
          openPreview(path);
        }
      });
      const dl = n.querySelector('.dl');
      if (dl) dl.addEventListener('click', (e) => { e.stopPropagation(); download(path); });
      n.querySelector('.rn').addEventListener('click', (e) => { e.stopPropagation(); rename(path); });
      n.querySelector('.del').addEventListener('click', (e) => { e.stopPropagation(); remove(path, isDir); });
    }
  }

  async function toggleExpand(nodeEl, path) {
    const holder = nodeEl.nextElementSibling;
    if (!holder || !holder.classList.contains('children')) return;
    if (expanded.has(path)) {
      expanded.delete(path);
      holder.classList.remove('open');
      nodeEl.querySelector('.chev').textContent = '▸';
      return;
    }
    expanded.add(path);
    holder.classList.add('open');
    nodeEl.querySelector('.chev').textContent = '▾';
    await loadChildren(holder, depthOf(path));
  }

  async function loadChildren(holder, parentDepth) {
    if (holder.dataset.loaded === '1') return;
    const path = holder.dataset.childrenOf;
    try {
      const entries = await listDir(path);
      holder.innerHTML = entries.map(e => nodeHTML(e, parentDepth + 1)).join('');
      bindNodes(holder);
      holder.dataset.loaded = '1';
    } catch (e) {
      holder.innerHTML = `<div style="padding:0.3rem 1rem;color:#f88;font-size:0.7rem">${escapeHTML(e.message)}</div>`;
    }
  }

  function download(path) {
    const a = document.createElement('a');
    a.href = '/api/fs/download?path=' + encodeURIComponent(path);
    a.style.display = 'none';
    document.body.appendChild(a);
    a.click();
    setTimeout(() => a.remove(), 0);
  }

  // ---- preview modal ----
  const previewEl  = document.getElementById('preview');
  const pvTitleEl  = previewEl.querySelector('.title');
  const pvBodyEl   = previewEl.querySelector('.body');
  const pvDlEl     = previewEl.querySelector('.dl');
  const pvCloseEl  = previewEl.querySelector('.close');

  // Dispatch by what the server says the Content-Type is. The server is
  // authoritative because it sniffs file bytes for extensionless files,
  // catching binaries that would otherwise show up as text gibberish.
  function kindFromContentType(ct) {
    ct = (ct || '').split(';')[0].trim().toLowerCase();
    if (ct.startsWith('image/')) return 'image';
    if (ct.startsWith('video/')) return 'video';
    if (ct.startsWith('audio/')) return 'audio';
    if (ct === 'application/pdf') return 'pdf';
    if (ct.startsWith('text/')) return 'text';
    return 'binary';
  }

  async function openPreview(path) {
    const name = path.split('/').pop();
    pvTitleEl.textContent = path;
    pvDlEl.href = '/api/fs/download?path=' + encodeURIComponent(path);
    pvBodyEl.innerHTML = '<div class="msg">loading…</div>';
    previewEl.classList.add('open');

    const url = '/api/fs/preview?path=' + encodeURIComponent(path);
    let ct = '';
    try {
      const r = await fetch(url, { method: 'HEAD' });
      if (!r.ok) {
        const t = await r.text();
        pvBodyEl.innerHTML = '<div class="msg err"></div>';
        pvBodyEl.querySelector('.msg').textContent = t.trim() || r.statusText;
        return;
      }
      ct = r.headers.get('Content-Type') || '';
    } catch (e) {
      pvBodyEl.innerHTML = '<div class="msg err"></div>';
      pvBodyEl.querySelector('.msg').textContent = 'preview failed: ' + e.message;
      return;
    }

    const kind = kindFromContentType(ct);
    try {
      if (kind === 'image') {
        pvBodyEl.innerHTML = '';
        const img = document.createElement('img');
        img.src = url; img.alt = name;
        img.onerror = () => { pvBodyEl.innerHTML = '<div class="msg err">image failed to load</div>'; };
        pvBodyEl.appendChild(img);
      } else if (kind === 'video') {
        pvBodyEl.innerHTML = '';
        const v = document.createElement('video');
        v.src = url; v.controls = true; v.autoplay = false;
        pvBodyEl.appendChild(v);
      } else if (kind === 'audio') {
        pvBodyEl.innerHTML = '';
        const a = document.createElement('audio');
        a.src = url; a.controls = true;
        pvBodyEl.appendChild(a);
      } else if (kind === 'pdf') {
        pvBodyEl.innerHTML = '';
        const f = document.createElement('iframe');
        f.src = url;
        pvBodyEl.appendChild(f);
      } else if (kind === 'text') {
        const r = await fetch(url);
        const txt = await r.text();
        pvBodyEl.innerHTML = '<pre></pre>';
        pvBodyEl.querySelector('pre').textContent = txt;
      } else {
        pvBodyEl.innerHTML = '<div class="msg">binary file (' + ct + ') — use ↓ download to save</div>';
      }
    } catch (e) {
      pvBodyEl.innerHTML = '<div class="msg err"></div>';
      pvBodyEl.querySelector('.msg').textContent = 'preview failed: ' + e.message;
    }
  }

  function closePreview() {
    previewEl.classList.remove('open');
    pvBodyEl.innerHTML = '';
  }

  pvCloseEl.addEventListener('click', closePreview);
  previewEl.addEventListener('click', (e) => {
    if (e.target === previewEl) closePreview();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && previewEl.classList.contains('open')) closePreview();
  });

  async function rename(path) {
    const oldName = path.split('/').pop();
    const next = prompt('Rename to (within same directory):', oldName);
    if (!next || next === oldName) return;
    if (next.includes('/')) {
      alert('cannot include "/" — drag the item if you want to move directories');
      return;
    }
    const parent = path.slice(0, path.length - oldName.length);
    const to = parent + next;
    try {
      await api('POST', '/api/fs/rename', {
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: 'from=' + encodeURIComponent(path) + '&to=' + encodeURIComponent(to),
      });
      refresh();
    } catch (e) { setStatus(e.message, true); }
  }

  async function remove(path, isDir) {
    if (!confirm(`Delete "${path}"${isDir ? ' (recursive)' : ''}?`)) return;
    try {
      const url = '/api/fs/remove?path=' + encodeURIComponent(path) + (isDir ? '&recursive=1' : '');
      await api('DELETE', url);
      refresh();
    } catch (e) { setStatus(e.message, true); }
  }

  async function mkdir() {
    const name = prompt('Folder name:');
    if (!name) return;
    if (name.includes('/')) { alert('name cannot contain "/"'); return; }
    const target = (cwd || '') + '/' + name;
    try {
      await api('POST', '/api/fs/mkdir', {
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: 'path=' + encodeURIComponent(target),
      });
      refresh();
    } catch (e) { setStatus(e.message, true); }
  }

  // --- drag/drop upload ---
  const dropTextEl = document.getElementById('fdrop-text');
  let dragDepth = 0;

  // Accept file drops anywhere on the page — the file panel just shows
  // visual feedback. text/html drags (e.g. selecting text) are ignored
  // because we only intercept events whose dataTransfer.types include 'Files'.
  const dropZone = document.body;

  dropZone.addEventListener('dragenter', (e) => {
    if (!e.dataTransfer || ![...e.dataTransfer.types].includes('Files')) return;
    e.preventDefault();
    dragDepth++;
    if (dropTextEl) dropTextEl.textContent = 'drop anywhere → ' + absCwd();
    fsEl.classList.add('dragging-over');
  });
  dropZone.addEventListener('dragleave', () => {
    dragDepth--;
    if (dragDepth <= 0) { dragDepth = 0; fsEl.classList.remove('dragging-over'); }
  });
  dropZone.addEventListener('dragover', (e) => {
    if (![...(e.dataTransfer?.types || [])].includes('Files')) return;
    e.preventDefault();
  });
  dropZone.addEventListener('drop', async (e) => {
    if (![...(e.dataTransfer?.types || [])].includes('Files')) return;
    e.preventDefault();
    dragDepth = 0;
    fsEl.classList.remove('dragging-over');
    const files = [...(e.dataTransfer?.files || [])];
    if (!files.length) return;
    const target = absCwd();
    setStatus(`uploading ${files.length} file(s) → ${target}...`);
    const fd = new FormData();
    fd.append('path', cwd || '/');
    for (const f of files) fd.append('file', f);
    try {
      const r = await fetch('/api/fs/upload', { method: 'POST', body: fd });
      if (!r.ok) throw new Error((await r.text()).trim() || r.statusText);
      const out = await r.json();
      setStatus(`uploaded ${out.saved?.length || files.length} file(s) → ${target}`);
      refresh();
    } catch (err) {
      setStatus('upload failed: ' + err.message, true);
    }
  });

  document.getElementById('fmkdir').addEventListener('click', mkdir);
  document.getElementById('frefresh').addEventListener('click', refresh);

  // --- resize handle ---
  const handle = document.getElementById('drag-handle');
  const stage = document.getElementById('stage');
  let dragging = false;
  handle.addEventListener('mousedown', () => { dragging = true; handle.classList.add('dragging'); document.body.style.userSelect = 'none'; });
  window.addEventListener('mouseup',   () => { if (!dragging) return; dragging = false; handle.classList.remove('dragging'); document.body.style.userSelect = ''; window.dispatchEvent(new Event('resize')); });
  window.addEventListener('mousemove', (e) => {
    if (!dragging) return;
    const stageRect = stage.getBoundingClientRect();
    const fromRight = stageRect.right - e.clientX;
    const min = 220, max = Math.max(min, stageRect.width * 0.6);
    const w = Math.min(max, Math.max(min, fromRight));
    fsEl.style.flexBasis = w + 'px';
  });

  // --- cwd sync: follow the terminal session's pane_current_path ---
  // Only active on /s/{id} pages; home page leaves this alone.
  function sessionIdFromPath() {
    const m = location.pathname.match(/^\/s\/([^\/?#]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  function relToRoot(abs) {
    if (!abs.startsWith(rootAbs)) return null;
    let rel = abs.slice(rootAbs.length).replace(/^\/+/, '');
    return rel;
  }

  let cwdSyncEnabled = true;
  let lastCwdServer = '';
  async function syncCwd() {
    const sid = sessionIdFromPath();
    if (!sid || !cwdSyncEnabled) return;
    try {
      const r = await fetch('/api/sessions/' + encodeURIComponent(sid) + '/cwd');
      if (!r.ok) return;
      const data = await r.json();
      const abs = (data.cwd || '').trim();
      if (!abs || abs === lastCwdServer) return;
      lastCwdServer = abs;
      const rel = relToRoot(abs);
      if (rel === null) return; // outside file panel root — ignore quietly
      if (rel === cwd) return;
      cwd = rel;
      expanded.clear();
      refresh();
    } catch (_) {}
  }

  (async function init() {
    try {
      const r = await fetch('/api/fs/root');
      if (r.ok) rootAbs = (await r.json()).root;
    } catch (_) {}
    await refresh();
    syncCwd();
    setInterval(syncCwd, 2000);
  })();
})();
