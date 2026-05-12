// AI session panel.
//
// Polls /api/ai/active to surface the live conversation: model in use,
// rolling context tokens, recent user prompts. Click a prompt to jump
// the terminal scrollback to where it appeared (via xterm's search addon).
(() => {
  const modelTag   = document.getElementById('ai-model');
  const ctxTag     = document.getElementById('ai-ctx');
  const panelEl    = document.getElementById('ai-panel');
  const projectEl  = document.getElementById('ai-project');
  const modelFull  = document.getElementById('ai-model-full');
  const ctxDetail  = document.getElementById('ai-ctx-detail');
  const ctxFill    = document.getElementById('ai-ctx-fill');
  const costDetail = document.getElementById('ai-cost-detail');
  const promptsEl  = document.getElementById('ai-prompts');
  const closeEl    = panelEl.querySelector('.head .close');

  function shortModel(m) {
    if (!m) return '—';
    // 'claude-opus-4-7' → 'opus-4-7'
    return m.replace(/^claude-/, '');
  }
  function fmtK(n) {
    if (!n) return '0';
    if (n < 1000) return String(n);
    if (n < 1_000_000) return (n / 1000).toFixed(1) + 'K';
    return (n / 1_000_000).toFixed(2) + 'M';
  }
  function fmtUSD(n) {
    if (n == null) return '—';
    if (n >= 100) return '$' + n.toFixed(0);
    if (n >= 10)  return '$' + n.toFixed(1);
    return '$' + n.toFixed(2);
  }

  // Latest fetched state, for panel rendering on click.
  let last = null;

  async function refresh() {
    try {
      const r = await fetch('/api/ai/active');
      if (!r.ok) return;
      const j = await r.json();
      if (!j || !j.file) {
        modelTag.textContent = '—';
        ctxTag.textContent = '—';
        return;
      }
      last = j;
      modelTag.textContent = shortModel(j.model);
      const ctx = j.context_tokens || 0;
      const win = j.context_window_est || 200000;
      const pct = Math.min(100, Math.round(ctx / win * 100));
      ctxTag.textContent = `${fmtK(ctx)}/${fmtK(win)} (${pct}%)`;
      ctxTag.style.color = pct > 85 ? '#df5b5b' : pct > 65 ? '#d6c25a' : '#bb8';
    } catch (_) {}
  }

  function openPanel() {
    if (!last) return;
    projectEl.textContent  = last.project || '—';
    modelFull.textContent  = last.model || '—';
    const ctx = last.context_tokens || 0;
    const win = last.context_window_est || 200000;
    const pct = Math.min(100, ctx / win * 100);
    ctxDetail.textContent = `${fmtK(ctx)} / ${fmtK(win)} (${pct.toFixed(1)}%)`;
    ctxFill.style.width = pct.toFixed(1) + '%';
    costDetail.textContent = fmtUSD(last.usage && last.usage.cost_usd);

    promptsEl.innerHTML = '';
    for (const p of (last.prompts || [])) {
      const li = document.createElement('li');
      const ts = p.timestamp ? new Date(p.timestamp).toLocaleString() : '';
      li.innerHTML = `<span class="ts">${ts}</span><span class="preview"></span>`;
      li.querySelector('.preview').textContent = p.preview;
      li.addEventListener('click', () => {
        panelEl.classList.remove('open');
        jumpToPrompt(p.preview);
      });
      promptsEl.appendChild(li);
    }
    panelEl.classList.add('open');
  }

  function closePanel() { panelEl.classList.remove('open'); }

  // Search the terminal scrollback for the start of the prompt and scroll
  // to it. Uses the search addon that app.js wires up.
  function jumpToPrompt(preview) {
    const search = window.roostSearchAddon;
    if (!search) {
      window.toast && window.toast('Search addon not ready', 'err');
      return;
    }
    // Take the first 30 chars of the prompt as a search needle.
    let needle = preview.split('\n')[0].trim();
    if (needle.length > 30) needle = needle.slice(0, 30);
    if (!needle) return;
    const found = search.findPrevious(needle, { decorations: { activeMatchBackground: '#4caf80' }});
    if (!found) {
      window.toast && window.toast(`Not in scrollback: "${needle}"`, 'info');
    }
  }

  modelTag.addEventListener('click', openPanel);
  ctxTag.addEventListener('click', openPanel);
  closeEl.addEventListener('click', closePanel);
  panelEl.addEventListener('click', (e) => { if (e.target === panelEl) closePanel(); });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && panelEl.classList.contains('open')) closePanel();
  });

  refresh();
  setInterval(refresh, 15000);
})();
