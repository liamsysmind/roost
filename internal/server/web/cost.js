// Tiny widget: pull today's Claude Code spend and stamp it onto #ai-cost.
//
// Loaded on every page that has the #ai-cost element. Refreshes every 30s
// and on click. Cost is parsed from /api/ai/usage?since=today.
(() => {
  const el = document.getElementById('ai-cost');
  if (!el) return;

  function fmtCost(c) {
    if (c >= 100) return '$' + c.toFixed(0);
    if (c >= 10)  return '$' + c.toFixed(1);
    return '$' + c.toFixed(2);
  }

  async function refresh() {
    try {
      const r = await fetch('/api/ai/usage?since=today');
      if (!r.ok) throw new Error(r.statusText);
      const j = await r.json();
      const u = j.usage || {};
      el.textContent = fmtCost(u.cost_usd || 0);
      el.title = `Claude Code today · ${u.messages || 0} msgs · `
        + `${(u.input_tokens / 1000).toFixed(1)}k in / `
        + `${(u.output_tokens / 1000).toFixed(1)}k out · `
        + `cache: ${(u.cache_read_tokens / 1000).toFixed(0)}k read, `
        + `${(u.cache_write_tokens / 1000).toFixed(0)}k write`;
    } catch (e) {
      el.textContent = '$?';
      el.title = 'cost unavailable: ' + e.message;
    }
  }

  el.addEventListener('click', refresh);
  refresh();
  setInterval(refresh, 30000);
})();
