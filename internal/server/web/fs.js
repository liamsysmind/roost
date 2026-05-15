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

  // --- progress bar ---
  const progEl     = document.getElementById('fprogress');
  const progLabel  = progEl.querySelector('.label');
  const progMeta   = progEl.querySelector('.meta');
  const progFill   = progEl.querySelector('.fill');
  const progCancel = progEl.querySelector('.cancel');
  let progStart    = 0;

  // showProgress takes an optional onCancel; when provided the ✕ button
  // becomes visible and clicking it invokes the callback (which is
  // expected to abort the underlying request).
  function showProgress(label, onCancel) {
    progStart = Date.now();
    progLabel.textContent = label;
    progFill.style.width = '0%';
    progMeta.textContent = '…';
    if (onCancel) {
      progCancel.onclick = onCancel;
      progEl.classList.add('cancellable');
    } else {
      progCancel.onclick = null;
      progEl.classList.remove('cancellable');
    }
    progEl.classList.add('active');
  }
  function updateProgress(loaded, total) {
    const pct = total > 0 ? (loaded / total) * 100 : 0;
    progFill.style.width = pct.toFixed(1) + '%';
    const secs = Math.max(0.1, (Date.now() - progStart) / 1000);
    const rate = loaded / secs;
    const eta  = total > loaded && rate > 0 ? Math.ceil((total - loaded) / rate) : 0;
    progMeta.textContent = total > 0
      ? `${fmtSize(loaded)}/${fmtSize(total)} · ${fmtSize(rate)}/s${eta ? ` · ${eta}s left` : ''}`
      : `${fmtSize(loaded)} · ${fmtSize(rate)}/s`;
  }
  function hideProgress() {
    progEl.classList.remove('cancellable');
    progCancel.onclick = null;
    setTimeout(() => progEl.classList.remove('active'), 250);
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

  // download streams the file via fetch so we can show progress, then
  // dispatches it to the browser's save flow as a blob. For files larger
  // than 2 GB the blob path can hit browser memory limits, so we fall back
  // to a plain <a download> link (no progress, but the browser handles
  // streaming directly).
  async function download(path) {
    const url = '/api/fs/download?path=' + encodeURIComponent(path);
    const filename = path.split('/').pop();
    const largeFileBytes = 2 * 1024 * 1024 * 1024;

    let total = 0;
    try {
      const head = await fetch(url, { method: 'HEAD' });
      if (head.ok) total = parseInt(head.headers.get('Content-Length') || '0', 10);
    } catch (_) {}
    if (total > largeFileBytes) {
      // bypass blob — let the browser stream it
      const a = document.createElement('a');
      a.href = url; a.download = filename; a.style.display = 'none';
      document.body.appendChild(a); a.click();
      setTimeout(() => a.remove(), 0);
      return;
    }

    showProgress(`Download ${filename}`);
    try {
      const r = await fetch(url);
      if (!r.ok) throw new Error((await r.text()).trim() || r.statusText);
      const reader = r.body.getReader();
      const chunks = [];
      let loaded = 0;
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        chunks.push(value);
        loaded += value.length;
        updateProgress(loaded, total || loaded);
      }
      hideProgress();
      const blob = new Blob(chunks);
      const obj = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = obj; a.download = filename; a.style.display = 'none';
      document.body.appendChild(a); a.click();
      setTimeout(() => { a.remove(); URL.revokeObjectURL(obj); }, 0);
    } catch (err) {
      hideProgress();
      const msg = 'Download failed: ' + err.message;
      setStatus(msg, true);
      window.toast && window.toast(msg, 'err');
    }
  }

  // ---- preview modal ----
  const previewEl  = document.getElementById('preview');
  const pvTitleEl  = previewEl.querySelector('.title');
  const pvBodyEl   = previewEl.querySelector('.body');
  const pvDlEl     = previewEl.querySelector('.dl');
  const pvCloseEl  = previewEl.querySelector('.close');

  // Map file extension → highlight.js language id. Anything not in here
  // falls through to hljs auto-detect, which is usually fine but slower.
  const langByExt = {
    go: 'go',
    py: 'python',
    rb: 'ruby',
    rs: 'rust',
    c: 'c', h: 'c',
    cpp: 'cpp', hpp: 'cpp', cc: 'cpp',
    java: 'java', kt: 'kotlin', swift: 'swift',
    js: 'javascript', mjs: 'javascript', cjs: 'javascript',
    ts: 'typescript', tsx: 'tsx', jsx: 'javascript',
    sh: 'bash', bash: 'bash', zsh: 'bash', fish: 'bash',
    json: 'json', yaml: 'yaml', yml: 'yaml', toml: 'toml',
    xml: 'xml', html: 'xml', htm: 'xml', css: 'css', scss: 'scss',
    sql: 'sql', diff: 'diff', patch: 'diff',
    ini: 'ini', conf: 'ini', cfg: 'ini',
    dockerfile: 'dockerfile',
    makefile: 'makefile',
  };
  function extToLang(name) {
    const lower = name.toLowerCase();
    if (lower === 'makefile' || lower === 'gnumakefile') return 'makefile';
    if (lower === 'dockerfile') return 'dockerfile';
    const m = lower.match(/\.([^.]+)$/);
    return m ? langByExt[m[1]] || null : null;
  }

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

  // Resolve an `src` from a markdown file (located at `mdPath`, relative to
  // the fs root) into an absolute fs-root-relative path. Returns null for
  // external URLs (http://…, data:, mailto:, etc.), which the caller should
  // leave alone. Handles "/abs", "rel/file", "./rel", and "../parent" forms,
  // and collapses .. segments lexically.
  function resolveMarkdownRelative(mdPath, src) {
    if (!src) return null;
    if (/^[a-z][a-z0-9+.-]*:/i.test(src) || src.startsWith('//') || src.startsWith('#')) {
      return null;
    }
    let rel;
    if (src.startsWith('/')) {
      rel = src.replace(/^\/+/, '');
    } else {
      const baseDir = mdPath.includes('/')
        ? mdPath.substring(0, mdPath.lastIndexOf('/'))
        : '';
      rel = baseDir ? baseDir + '/' + src : src;
    }
    const parts = [];
    for (const seg of rel.split('/')) {
      if (seg === '' || seg === '.') continue;
      if (seg === '..') { if (parts.length) parts.pop(); continue; }
      parts.push(seg);
    }
    return parts.join('/');
  }

  function rewriteMarkdownAssets(root, mdPath) {
    root.querySelectorAll('img').forEach((img) => {
      const src = img.getAttribute('src');
      const rel = resolveMarkdownRelative(mdPath, src);
      if (rel === null) return;
      img.src = '/api/fs/preview?path=' + encodeURIComponent(rel);
    });
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
        const isMarkdown = /\.(md|markdown|mdown|mkd)$/i.test(name);
        if (isMarkdown && window.marked) {
          // Trusted single-user filesystem — render markdown as-is.
          // If we ever expand to multi-user, swap in DOMPurify here.
          window.marked.setOptions({ gfm: true, breaks: false });
          pvBodyEl.innerHTML = '<div class="prose"></div>';
          const proseEl = pvBodyEl.querySelector('.prose');
          proseEl.innerHTML = window.marked.parse(txt);
          // Rewrite relative <img src=…> so embedded screenshots load
          // through the fs API instead of resolving against the current
          // page URL. Markdown that references "docs/img/foo.png" expects
          // the link to be relative to the .md file, not to /s/{id}/.
          rewriteMarkdownAssets(proseEl, path);
          // Highlight every fenced code block inside the rendered HTML.
          if (window.hljs) {
            proseEl.querySelectorAll('pre code').forEach((b) => {
              try { hljs.highlightElement(b); } catch (_) {}
            });
          }
        } else {
          pvBodyEl.innerHTML = '<pre><code></code></pre>';
          const code = pvBodyEl.querySelector('code');
          code.textContent = txt;
          if (window.hljs) {
            const lang = extToLang(name);
            if (lang) code.className = 'language-' + lang;
            try { hljs.highlightElement(code); } catch (_) {}
          }
        }
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

  function reportError(prefix, e) {
    const msg = prefix + ': ' + e.message;
    setStatus(msg, true);
    window.toast && window.toast(msg, 'err');
  }

  async function rename(path) {
    const oldName = path.split('/').pop();
    const next = prompt('Rename to (within same directory):', oldName);
    if (!next || next === oldName) return;
    if (next.includes('/')) {
      window.toast && window.toast('Name cannot include "/"', 'err');
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
    } catch (e) { reportError('Rename failed', e); }
  }

  async function remove(path, isDir) {
    if (!confirm(`Delete "${path}"${isDir ? ' (recursive)' : ''}?`)) return;
    try {
      const url = '/api/fs/remove?path=' + encodeURIComponent(path) + (isDir ? '&recursive=1' : '');
      await api('DELETE', url);
      refresh();
    } catch (e) { reportError('Delete failed', e); }
  }

  async function mkdir() {
    const name = prompt('Folder name:');
    if (!name) return;
    if (name.includes('/')) {
      window.toast && window.toast('Name cannot include "/"', 'err');
      return;
    }
    const target = (cwd || '') + '/' + name;
    try {
      await api('POST', '/api/fs/mkdir', {
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: 'path=' + encodeURIComponent(target),
      });
      refresh();
    } catch (e) { reportError('Mkdir failed', e); }
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
  // Confirm threshold: multi-file uploads, or any single file ≥ 100 MB.
  // Single small-file drops stay frictionless; the threshold catches the
  // accident that motivated this gate (dragging multi-GB artifacts in by
  // mistake — server would happily stream all of them with no UI off-ramp).
  const CONFIRM_BYTES = 100 * 1024 * 1024;

  async function uploadFiles(files) {
    if (!files.length) return;
    const target = absCwd();
    const totalBytes = files.reduce((s, f) => s + (f.size || 0), 0);

    const needsConfirm = files.length > 1 || (files[0]?.size || 0) >= CONFIRM_BYTES;
    if (needsConfirm) {
      const ok = await confirmUpload(files, target, totalBytes);
      if (!ok) {
        setIdleStatus('upload cancelled');
        return;
      }
    }

    const label = files.length === 1
      ? `Upload ${files[0].name} → ${target}`
      : `Upload ${files.length} files → ${target}`;
    setStatus(`uploading ${files.length} file(s) → ${target}...`);

    const fd = new FormData();
    fd.append('path', cwd || '/');
    for (const f of files) fd.append('file', f);

    const job = xhrPost('/api/fs/upload', fd, (loaded) => {
      updateProgress(loaded, totalBytes);
    });
    showProgress(label, () => job.abort());

    try {
      const respText = await job.promise;
      const out = JSON.parse(respText);
      hideProgress();
      setStatus(`uploaded ${out.saved?.length || files.length} file(s) → ${target}`);
      window.toast && window.toast(`Uploaded ${out.saved?.length || files.length} file(s) → ${target}`, 'ok');
      refresh();
    } catch (err) {
      hideProgress();
      if (err && err.message === 'cancelled') {
        // Aborting the XHR closes the TCP connection mid-multipart; the
        // server's Save() returns an error and removes the partial
        // .roost-upload temp file, so the destination directory stays
        // clean.
        setIdleStatus('upload cancelled');
        window.toast && window.toast('Upload cancelled', 'ok');
        refresh();
        return;
      }
      const msg = 'Upload failed: ' + err.message;
      setStatus(msg, true);
      window.toast && window.toast(msg, 'err');
    }
  }

  // XHR-based POST so we get upload.onprogress — fetch can't measure
  // request body progress in any current browser. Returns { promise, abort }
  // so callers can both await completion and wire an abort button.
  function xhrPost(url, body, onProgress) {
    const xhr = new XMLHttpRequest();
    let aborted = false;
    const promise = new Promise((resolve, reject) => {
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) onProgress(e.loaded, e.total);
      };
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) resolve(xhr.responseText);
        else reject(new Error((xhr.responseText || '').trim() || `HTTP ${xhr.status}`));
      };
      xhr.onabort = () => reject(new Error('cancelled'));
      xhr.onerror = () => reject(new Error(aborted ? 'cancelled' : 'network error'));
      xhr.ontimeout = () => reject(new Error('timeout'));
      xhr.open('POST', url);
      xhr.send(body);
    });
    return {
      promise,
      abort: () => { aborted = true; xhr.abort(); },
    };
  }

  // --- upload confirm modal ---
  const confEl     = document.getElementById('fconfirm');
  const confDest   = confEl.querySelector('.dest code');
  const confTotal  = confEl.querySelector('.total');
  const confFiles  = confEl.querySelector('.files');
  const confOk     = confEl.querySelector('.foot .ok');
  const confCancel = confEl.querySelector('.foot .cancel');
  const confClose  = confEl.querySelector('.head .close');

  function confirmUpload(files, dest, totalBytes) {
    confDest.textContent = dest;
    confTotal.textContent = `${files.length} file${files.length === 1 ? '' : 's'} · ${fmtSize(totalBytes)} total`;
    confFiles.innerHTML = '';
    const cap = 12;
    const visible = files.slice(0, cap);
    for (const f of visible) {
      const li = document.createElement('li');
      const n = document.createElement('span'); n.className = 'n'; n.textContent = f.name;
      const s = document.createElement('span'); s.className = 's'; s.textContent = fmtSize(f.size || 0);
      li.appendChild(n); li.appendChild(s);
      confFiles.appendChild(li);
    }
    if (files.length > visible.length) {
      const li = document.createElement('li');
      li.className = 'more';
      li.textContent = `…and ${files.length - visible.length} more`;
      confFiles.appendChild(li);
    }
    confEl.classList.add('open');

    return new Promise((resolve) => {
      const cleanup = (result) => {
        confEl.classList.remove('open');
        confOk.onclick = null;
        confCancel.onclick = null;
        confClose.onclick = null;
        document.removeEventListener('keydown', onKey, true);
        resolve(result);
      };
      const onKey = (e) => {
        if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); cleanup(false); }
        else if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); cleanup(true); }
      };
      confOk.onclick = () => cleanup(true);
      confCancel.onclick = () => cleanup(false);
      confClose.onclick = () => cleanup(false);
      // Capture-phase keydown so xterm.js (terminal focus) can't swallow Esc/Enter.
      document.addEventListener('keydown', onKey, true);
      setTimeout(() => confOk.focus(), 0);
    });
  }

  dropZone.addEventListener('drop', async (e) => {
    if (![...(e.dataTransfer?.types || [])].includes('Files')) return;
    e.preventDefault();
    dragDepth = 0;
    fsEl.classList.remove('dragging-over');
    await uploadFiles([...(e.dataTransfer?.files || [])]);
  });

  // Ctrl+V file paste: Windows Explorer / macOS Finder "Copy" + Ctrl+V on the
  // browser fires a paste event whose clipboardData.files holds the actual
  // File objects. We listen in the capture phase so xterm.js (terminal focus)
  // doesn't consume the event before we see it. Pure text pastes have no
  // files attached and pass through to whichever handler claims them next.
  window.addEventListener('paste', (e) => {
    const files = [...(e.clipboardData?.files || [])];
    if (!files.length) return;
    e.preventDefault();
    e.stopPropagation();
    uploadFiles(files);
  }, true);

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
  // Resolves the session id from either /s/{id} or /#{id} — must mirror
  // app.js's resolveSessionID exactly, otherwise the file tree and AI panel
  // silently drop cwd sync for fragment-style URLs (which is what visiting
  // the root '/' auto-rewrites to).
  function sessionIdFromPath() {
    const m = location.pathname.match(/^\/s\/([^\/?#]+)/);
    if (m) return decodeURIComponent(m[1]);
    if (location.hash.length > 1) return decodeURIComponent(location.hash.slice(1));
    return '';
  }

  function relToRoot(abs) {
    if (!abs.startsWith(rootAbs)) return null;
    let rel = abs.slice(rootAbs.length).replace(/^\/+/, '');
    return rel;
  }

  let cwdSyncEnabled = true;
  let lastCwdServer = '';
  let lastCmdServer = '';
  let lastAppServer = '';
  async function syncCwd() {
    const sid = sessionIdFromPath();
    if (!sid || !cwdSyncEnabled) return;
    try {
      const r = await fetch('/api/sessions/' + encodeURIComponent(sid) + '/cwd');
      if (!r.ok) return;
      const data = await r.json();
      const cmd = (data.command || '').trim();
      if (cmd !== lastCmdServer) {
        lastCmdServer = cmd;
        window.dispatchEvent(new CustomEvent('roost-pane-cmd-changed', {
          detail: { command: cmd },
        }));
      }
      const app = (data.app || '').trim();
      if (app !== lastAppServer) {
        lastAppServer = app;
        window.dispatchEvent(new CustomEvent('roost-pane-app-changed', {
          detail: { app: app },
        }));
      }
      const abs = (data.cwd || '').trim();
      if (!abs || abs === lastCwdServer) return;
      const prev = lastCwdServer;
      lastCwdServer = abs;
      // Tell other panels (AI tab) so they can re-resolve immediately
      // instead of waiting for their own poll tick.
      window.dispatchEvent(new CustomEvent('roost-cwd-changed', {
        detail: { previous: prev, current: abs },
      }));
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
