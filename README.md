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

_Coming soon — see [milestones](#milestones)._

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
