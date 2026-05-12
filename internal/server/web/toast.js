// Toast notifications — floating, top-right, auto-dismiss.
// Exposed as window.toast(msg, kind?) where kind ∈ {'err','ok','info'}.
(() => {
  let container;
  function ensureContainer() {
    if (container) return container;
    container = document.createElement('div');
    container.id = 'toasts';
    document.body.appendChild(container);
    const style = document.createElement('style');
    style.textContent = `
      #toasts {
        position: fixed; top: 38px; right: 12px; z-index: 200;
        display: flex; flex-direction: column; gap: 6px;
        pointer-events: none; max-width: 360px;
      }
      #toasts .toast {
        background: #2a1414; border: 1px solid #844; color: #f88;
        padding: 10px 14px; border-radius: 6px; font-size: 0.82rem;
        box-shadow: 0 4px 16px rgba(0,0,0,0.6);
        pointer-events: auto; cursor: pointer;
        animation: roost-toast-in 0.18s ease;
        white-space: pre-wrap; word-break: break-word;
      }
      #toasts .toast.info { background: #14202a; border-color: #466; color: #9cc; }
      #toasts .toast.ok   { background: #142a18; border-color: #484; color: #afa; }
      @keyframes roost-toast-in {
        from { transform: translateX(16px); opacity: 0; }
        to   { transform: translateX(0);    opacity: 1; }
      }
    `;
    document.head.appendChild(style);
    return container;
  }

  function toast(message, kind) {
    const root = ensureContainer();
    const el = document.createElement('div');
    el.className = 'toast' + (kind ? ' ' + kind : '');
    el.textContent = message;
    const dismiss = () => { el.remove(); };
    el.addEventListener('click', dismiss);
    root.appendChild(el);
    const dwell = kind === 'ok' || kind === 'info' ? 3000 : 6500;
    setTimeout(dismiss, dwell);
  }

  window.toast = toast;
})();
