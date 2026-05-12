// AI session pane.
//
// Lives in the right-side tab strip alongside the file tree. Polls
// /api/ai/active and renders the live conversation: model in use,
// rolling context tokens, recent user prompts. Click a prompt to jump
// the terminal scrollback to where it appeared.
(() => {
  const modelTag    = document.getElementById('ai-model');
  const ctxTag      = document.getElementById('ai-ctx');
  const projectEl   = document.getElementById('ai-project');
  const modelFull   = document.getElementById('ai-model-full');
  const ctxDetail   = document.getElementById('ai-ctx-detail');
  const ctxFill     = document.getElementById('ai-ctx-fill');
  const msgsDetail  = document.getElementById('ai-msgs-detail');
  const tokensDetail = document.getElementById('ai-tokens-detail');
  const promptsEl   = document.getElementById('ai-prompts');
  const badgeEl     = document.getElementById('ai-tab-badge');

  function shortModel(m) {
    if (!m) return '—';
    return m.replace(/^claude-/, '');
  }
  function fmtK(n) {
    if (!n) return '0';
    if (n < 1000) return String(n);
    if (n < 1_000_000) return (n / 1000).toFixed(1) + 'K';
    return (n / 1_000_000).toFixed(2) + 'M';
  }

  function sessionIdFromPath() {
    const m = location.pathname.match(/^\/s\/([^\/?#]+)/);
    return m ? decodeURIComponent(m[1]) : '';
  }

  async function refresh() {
    try {
      const sid = sessionIdFromPath();
      const url = sid
        ? '/api/ai/active?session=' + encodeURIComponent(sid)
        : '/api/ai/active';
      const r = await fetch(url);
      if (!r.ok) return;
      const j = await r.json();
      if (!j || !j.file) {
        modelTag.textContent = '—';
        ctxTag.textContent = '—';
        projectEl.textContent = '—';
        modelFull.textContent = '—';
        ctxDetail.textContent = '—';
        ctxFill.style.width = '0%';
        msgsDetail.textContent = '—';
        tokensDetail.textContent = '—';
        promptsEl.innerHTML = '<div class="empty">no Claude Code session for the terminal\'s current directory</div>';
        if (badgeEl) { badgeEl.hidden = true; }
        return;
      }
      const ctx = j.context_tokens || 0;
      const win = j.context_window_est || 200000;
      const pct = Math.min(100, ctx / win * 100);

      modelTag.textContent = shortModel(j.model);
      ctxTag.textContent = `${fmtK(ctx)}/${fmtK(win)} (${pct.toFixed(0)}%)`;
      ctxTag.style.color = pct > 85 ? '#df5b5b' : pct > 65 ? '#d6c25a' : '#bb8';

      projectEl.textContent  = j.project || '—';
      modelFull.textContent  = j.model || '—';
      ctxDetail.textContent  = `${fmtK(ctx)} / ${fmtK(win)} (${pct.toFixed(1)}%)`;
      ctxFill.style.width    = pct.toFixed(1) + '%';
      const u = j.usage || {};
      msgsDetail.textContent = String(u.messages || 0);
      tokensDetail.textContent =
        `in ${fmtK(u.input_tokens)} · out ${fmtK(u.output_tokens)} · ` +
        `cache write ${fmtK(u.cache_write_tokens)} · cache read ${fmtK(u.cache_read_tokens)}`;

      const prompts = j.prompts || [];
      if (prompts.length === 0) {
        promptsEl.innerHTML = '<div class="empty">no prompts yet</div>';
      } else {
        promptsEl.innerHTML = '';
        for (const p of prompts) {
          const li = document.createElement('li');
          const ts = p.timestamp ? new Date(p.timestamp).toLocaleString() : '';
          li.innerHTML = `<span class="ts">${ts}</span><span class="preview"></span>`;
          li.querySelector('.preview').textContent = p.preview;
          li.addEventListener('click', () => jumpToPrompt(p.preview));
          promptsEl.appendChild(li);
        }
      }
      if (badgeEl) {
        badgeEl.hidden = prompts.length === 0;
        badgeEl.textContent = prompts.length;
      }
    } catch (_) {}
  }

  function jumpToPrompt(preview) {
    const search = window.roostSearchAddon;
    if (!search) {
      window.toast && window.toast('Search addon not ready', 'err');
      return;
    }
    let needle = preview.split('\n')[0].trim();
    if (needle.length > 30) needle = needle.slice(0, 30);
    if (!needle) return;
    const found = search.findPrevious(needle, { decorations: { activeMatchBackground: '#4caf80' }});
    if (!found) {
      window.toast && window.toast(`Not in scrollback: "${needle}"`, 'info');
    }
  }

  // --- right-panel tab switching ---
  // Exposed so the top-bar chips can switch to the AI tab on click.
  function setTab(name) {
    document.querySelectorAll('.ftab').forEach(b => b.classList.toggle('active', b.dataset.tab === name));
    document.querySelectorAll('.ftab-pane').forEach(p => p.classList.toggle('active', p.id === name + '-pane'));
    if (name === 'ai') refresh();
    // The file tree fits to whatever width it currently has; AI doesn't need re-fit.
    window.dispatchEvent(new Event('resize'));
  }
  window.roostSetRightTab = setTab;

  for (const btn of document.querySelectorAll('.ftab')) {
    btn.addEventListener('click', () => setTab(btn.dataset.tab));
  }

  modelTag.addEventListener('click', () => setTab('ai'));
  ctxTag.addEventListener('click', () => setTab('ai'));

  refresh();
  setInterval(refresh, 15000);
})();
