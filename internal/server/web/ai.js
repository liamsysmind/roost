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

  // Pane-state tracking — both come from fs.js's cwd poll (piggybacked).
  //   currentApp: "claude" | "codex" | "" (no AI agent in the pane's tree)
  //   currentCmd: pane's foreground process name (used to detect TUI apps)
  //
  // We gate two things on these:
  //   - AI prompt list: hidden unless we're actually in Claude / Codex. This
  //     prevents stale prompts from an old JSONL showing up in a fresh bash
  //     shell that happens to share the cwd.
  //   - Anchor click handler: disabled while a TUI app (vim, less, htop) is
  //     redrawing the buffer — jumping into a moving viewport is pointless
  //     and the buffer state isn't stable anyway.
  const SHELLS = new Set(['bash', 'sh', 'zsh', 'fish', 'dash']);
  let currentApp = '';
  let currentCmd = '';
  function inAIAgent() {
    return currentApp === 'claude' || currentApp === 'codex';
  }
  function recomputeAnchorEnabled() {
    const on = inAIAgent() || SHELLS.has(currentCmd.toLowerCase());
    promptsEl.classList.toggle('anchor-disabled', !on);
  }
  function anchorEnabled() {
    return !promptsEl.classList.contains('anchor-disabled');
  }
  window.addEventListener('roost-pane-cmd-changed', (e) => {
    currentCmd = ((e.detail && e.detail.command) || '').toLowerCase();
    recomputeAnchorEnabled();
  });
  window.addEventListener('roost-pane-app-changed', (e) => {
    currentApp = (e.detail && e.detail.app) || '';
    recomputeAnchorEnabled();
    refresh(); // re-render so AI prompts appear/disappear immediately
  });

  async function refresh() {
    try {
      const sid = sessionIdFromPath();
      const params = new URLSearchParams();
      if (sid) params.set('session', sid);
      if (currentApp) params.set('app', currentApp);
      const url = '/api/ai/active' + (params.toString() ? '?' + params.toString() : '');
      const r = await fetch(url);
      if (!r.ok) return;
      const j = await r.json();
      if (!j || !j.file) {
        modelTag.textContent = '—';
        ctxTag.textContent = '—';
        projectEl.textContent = '—';
        modelFull.textContent = '—';
        ctxDetail.textContent = '—';
        msgsDetail.textContent = '—';
        tokensDetail.textContent = '—';
        // Still scan terminal buffer for shell commands so the anchor list
        // is useful even when there's no Claude Code session for this cwd.
        renderAnchors([]);
        return;
      }
      const ctx = j.context_tokens || 0;

      modelTag.textContent = shortModel(j.model);
      ctxTag.textContent = fmtK(ctx);

      projectEl.textContent  = j.project || '—';
      modelFull.textContent  = j.model || '—';
      ctxDetail.textContent  = fmtK(ctx);
      const u = j.usage || {};
      msgsDetail.textContent = String(u.messages || 0);
      tokensDetail.textContent =
        `in ${fmtK(u.input_tokens)} · out ${fmtK(u.output_tokens)} · ` +
        `cache write ${fmtK(u.cache_write_tokens)} · cache read ${fmtK(u.cache_read_tokens)}`;

      renderAnchors(j.prompts || []);
    } catch (_) {}
  }

  function gateClick(handler) {
    return () => {
      if (!anchorEnabled()) {
        window.toast && window.toast(
          'Anchor jump disabled while a TUI app is running — exit it first.',
          'info');
        return;
      }
      handler();
    };
  }

  function renderAnchors(prompts) {
    promptsEl.innerHTML = '';

    // AI prompt anchors (from Claude Code JSONL). Only show when we know the
    // user is actually inside Claude / Codex right now — otherwise we'd be
    // surfacing stale prompts from a previous session that just happened to
    // share this cwd.
    const showPrompts = inAIAgent() && prompts.length > 0;
    if (showPrompts) {
      const head = document.createElement('li');
      head.className = 'section-head';
      head.textContent = 'AI prompts';
      promptsEl.appendChild(head);
      for (const p of prompts) {
        const li = document.createElement('li');
        const ts = p.timestamp ? new Date(p.timestamp).toLocaleString() : '';
        li.innerHTML = `<span class="ts">${ts}</span><span class="preview"></span>`;
        li.querySelector('.preview').textContent = p.preview;
        li.addEventListener('click', gateClick(() => jumpToPrompt(p.preview)));
        promptsEl.appendChild(li);
      }
    }

    // Shell command anchors (scanned from terminal buffer).
    const cmds = scanShellCommands(30);
    if (cmds.length > 0) {
      const head = document.createElement('li');
      head.className = 'section-head';
      head.textContent = 'Shell commands';
      promptsEl.appendChild(head);
      for (const c of cmds) {
        const li = document.createElement('li');
        li.className = 'shell-cmd';
        li.innerHTML = '<span class="preview"></span>';
        // Render "$ command" as a single line — the prompt marker and the
        // command read better together than stacked.
        li.querySelector('.preview').textContent = '$ ' + c.command;
        li.addEventListener('click', gateClick(() => scrollToRow(c.row)));
        promptsEl.appendChild(li);
      }
    }

    const aiCount = showPrompts ? prompts.length : 0;
    if (aiCount === 0 && cmds.length === 0) {
      promptsEl.innerHTML = '<div class="empty">no anchors in scrollback yet</div>';
    }

    if (badgeEl) {
      const total = aiCount + cmds.length;
      badgeEl.hidden = total === 0;
      badgeEl.textContent = total;
    }
  }

  // Iterate buffer bottom-up, yielding each "logical line" (head line plus
  // any forward wrap continuations) joined into a single string along with
  // the head row index. Caller stops by returning truthy from `visit`.
  function walkLogicalLines(term, visit) {
    const buf = term.buffer.active;
    const total = buf.length;
    for (let i = total - 1; i >= 0; i--) {
      const line = buf.getLine(i);
      if (!line || line.isWrapped) continue;
      let text = line.translateToString(true);
      let j = i + 1;
      while (j < total) {
        const next = buf.getLine(j);
        if (!next || !next.isWrapped) break;
        text += next.translateToString(true);
        j++;
      }
      if (visit(text, i)) return;
    }
  }

  // Scroll viewport so `row` sits about a third of the way down (context
  // above and below), then briefly highlight the whole row as a visual anchor.
  function scrollToRow(row) {
    const term = window.roostTerm;
    if (!term) return;
    const buf = term.buffer.active;
    const viewportY = buf.viewportY;
    const target = Math.max(0, row - Math.floor(term.rows / 3));
    term.scrollLines(target - viewportY);
    term.select(0, row, term.cols);
    setTimeout(() => {
      const sel = term.getSelectionPosition && term.getSelectionPosition();
      if (sel && sel.start && sel.start.y === row && sel.start.x === 0) {
        term.clearSelection();
      }
    }, 4000);
  }

  function jumpToPrompt(preview) {
    const term = window.roostTerm;
    if (!term) {
      window.toast && window.toast('Terminal not ready', 'err');
      return;
    }
    let needle = preview.split('\n')[0].trim();
    if (!needle) return;
    if (needle.length > 30) needle = needle.slice(0, 30);

    // Manual buffer scan (joins wrap continuations) — xterm.js's SearchAddon
    // misses matches that span a wrap boundary, which Codex/Claude Code's
    // boxed prompts hit constantly.
    let foundRow = -1;
    walkLogicalLines(term, (text, row) => {
      if (text.indexOf(needle) >= 0) {
        foundRow = row;
        return true;
      }
    });

    if (foundRow < 0) {
      window.toast && window.toast(`Prompt not in terminal scrollback: "${needle}"`, 'info');
      return;
    }
    scrollToRow(foundRow);
  }

  // Match a typical interactive shell prompt:
  //   user@host:path$ command
  //   user@host:path# command   (root)
  // Returns null if the line doesn't look like a shell prompt + command. The
  // pattern is intentionally narrow — matching looser patterns like "* $ *"
  // would catch arbitrary text and pollute the anchor list.
  const SHELL_PROMPT_RE = /^[\w][\w.-]*@[\w.-]+:[^\s$#]*[$#]\s+(\S.*)$/;
  function scanShellCommands(max) {
    const term = window.roostTerm;
    if (!term) return [];
    const out = [];
    walkLogicalLines(term, (text, row) => {
      const m = text.match(SHELL_PROMPT_RE);
      if (m && m[1].length <= 200) {
        out.push({ row, command: m[1].trim() });
        if (out.length >= max) return true;
      }
    });
    return out;
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

  // fs.js fires this whenever the terminal's tmux cwd changes — refresh
  // immediately so the AI panel doesn't keep showing the previous
  // project's data after the user cd's away.
  window.addEventListener('roost-cwd-changed', refresh);

  refresh();
  // Backup poll — handles cases where the cwd event didn't fire
  // (e.g. session created after page load).
  setInterval(refresh, 5000);
})();
