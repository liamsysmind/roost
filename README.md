# roost

A self-hosted workspace where your AI agents come to roost.

`roost` runs as a single binary on your dev box. Open any browser, connect over SSH
tunnel (or your private network), and you get a unified workspace: terminal,
file tree, AI dashboard. One page, one process, every platform.

Built for developers who live in `ssh` and let agents (Claude Code, Codex, Aider…)
do most of the typing.

## Status

Pre-alpha. Active development. Not yet usable.

## Why

Existing options didn't fit:

- **Warp** — cloud subscription, not self-hosted.
- **Cursor / Windsurf** — IDE-centric, not terminal-first.
- **ttyd / gotty** — terminal only, no file management, no AI integration.
- **filebrowser** — file management only.
- **Tabby / Wave** — desktop apps, native install per OS.
- **code-server** — VS Code in a browser; great, but VS Code-shaped.

There was no "terminal + file tree + AI dashboard, web-based, self-hosted"
option. So this exists.

## Design

- **Single Go binary**. `scp` it to your dev box, `./roost` runs it.
- **Browser frontend**. xterm.js for the terminal, Svelte for the rest.
- **SSH tunnel friendly**. Listens on `127.0.0.1` by default.
- **AI-agent aware**. Reads Claude Code / Codex session state. Cost panel,
  notifications when an agent stops waiting, prompt template launcher.
- **Cross-platform by default**. Any modern browser; the server side runs
  wherever Go cross-compiles (Linux, macOS, Windows).

## Quickstart

```bash
# 1. Build
git clone https://github.com/liamsysmind/roost && cd roost
go build -o roost ./cmd/roost

# 2. One-time setup: password + session secret → ~/.config/roost/config.toml
./roost setup
# (or non-interactive)
echo 'your-password' | ./roost setup --password-stdin

# 3. Run (binds 127.0.0.1:8080 by default — only reachable via SSH tunnel)
./roost serve
```

From your laptop:
```bash
ssh -L 8080:localhost:8080 user@your-dev-box
# Then open http://localhost:8080 in your browser.
```

### Feature scope today (W1 + W1.5)

- Password-gated single-user login
- Cookie session (in-memory, 24h TTL)
- Full xterm.js terminal connected to a PTY on the server
- WebGL renderer + window resize
- Binds to `127.0.0.1` only — designed for SSH tunnel deployment
- **Persistent terminal sessions**: close your laptop, reopen the page tomorrow,
  the shell is still running and the scrollback is replayed.
- **Multi-tab sync**: open the same session in two tabs, both see the same output.
- **Effectively unbounded scrollback**: every session writes to a disk log file
  under `~/.local/share/roost/sessions/`. The full log grows with disk space.
  How much of the tail is replayed when a new client attaches is configured
  via `session.replay_kb` (default 4 MB; set to `0` to replay the entire log).

## Milestones

| Week | Scope |
|------|-------|
| W1 | HTTP server + WebSocket PTY + xterm.js terminal pane |
| W2 | File API + Svelte split layout + file tree + drag-drop |
| W3 | AI dashboard: cost panel, session selector, prompt templates |
| W4 | Stop-hook integration + Web Notifications + multi-tab session sync |
| W5 | Cross-compile, deploy docs, release tooling |

## License

MIT. See [LICENSE](LICENSE).
