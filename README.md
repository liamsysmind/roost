# roost

A self-hosted workspace where your AI agents come to roost.

`roost` runs as a single binary on your dev box. Open any browser, connect over
SSH tunnel (or your private network), and you get a unified workspace:

- **Persistent shells.** Real tmux-backed sessions that survive WebSocket
  disconnects, server restarts, even idle timeouts. Close the laptop, come
  back tomorrow, the build is still running.
- **Right-side file tree.** Browse, download, drag-and-drop upload, mkdir,
  rename, delete — straight from the terminal page, no second app.
- **AI cost panel.** Pulls today's Claude Code spend from `~/.claude/` and
  stamps it on the top bar. Tooltip breaks down tokens by category.
- **Push notifications.** SSE stream → Web Notification API toast. Wire your
  Claude Code Stop hook to `roost hook-info`'s snippet and your laptop pings
  when an agent stops waiting for you.
- **Multi-session, multi-tab.** Each browser tab is its own named session.
  Two tabs on the same session = live shared screen.

> Status: pre-alpha, single-user, designed for SSH-tunnel-first deployment.

## Quickstart

### 1. Requirements

- Go 1.23+ (only for building from source)
- `tmux` 3.0+ (required at runtime; roost refuses to start without it)
- A modern browser

### 2. Build & install

```bash
git clone https://github.com/liamsysmind/roost && cd roost
make
sudo make install            # installs to /usr/local/bin/roost
```

Or cross-compile prebuilt binaries for every supported target:

```bash
make cross                   # writes dist/roost-VERSION-OS-ARCH
```

### 3. One-time setup

```bash
roost setup                  # interactive: prompts for a password
# or non-interactive:
echo 'your-password' | roost setup --password-stdin
```

Writes `~/.config/roost/config.toml` with the password hash, session secret,
and hook secret. Defaults are sane — edit only if you need a different listen
address, log dir, or filesystem root.

### 4. Run

```bash
roost serve
```

Listens on `127.0.0.1:8080` — the SSH-tunnel-first default. From your laptop:

```bash
ssh -L 8080:localhost:8080 user@your-dev-box
# Then open http://localhost:8080 in your browser.
```

## Configuration

`~/.config/roost/config.toml`:

```toml
[auth]
password_hash  = "..."        # bcrypt; rewrite via `roost setup --force`
session_secret = "..."        # used to mint session ids; rotate to log everyone out
hook_secret    = "..."        # required header on POST /api/notify

[server]
addr = "127.0.0.1:8080"       # bind here; SSH tunnel into it

[session]
log_dir   = ""                # default: $XDG_DATA_HOME/roost/sessions
                              #   or ~/.local/share/roost/sessions
replay_kb = 4096              # how many KB of the log to send on attach.
                              #   0 = full log (effectively unbounded)
idle_ttl  = "24h"             # GC sessions with no clients after this idle.
                              #   tmux session survives even after GC.

[fs]
root = ""                     # default: your home directory.
                              #   /api/fs/* operations are contained here.
```

## Wiring up notifications

Get the ready-to-paste snippet:

```bash
roost hook-info
```

It prints a JSON block to add to `~/.claude/settings.json`. Once configured,
Claude Code's `Stop` hook fires a curl POST to `roost`, which fan-outs over
SSE to every connected browser. Native OS toast lights up.

## Deploying as a service

### Linux (systemd, user unit)

```bash
mkdir -p ~/.config/systemd/user
cp dist/systemd/roost.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now roost
```

### macOS (launchd)

```bash
cp dist/launchd/com.liamsysmind.roost.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.liamsysmind.roost.plist
```

## What this isn't

- **Not multi-tenant.** Single password, single user. Add a reverse-proxy
  with its own auth if you want per-user accounts.
- **Not exposed to the internet by default.** It binds to `127.0.0.1`. You
  reach it via SSH tunnel or your private network (Tailscale, ZeroTier, …).
- **Not VS Code.** No editor pane. The file tree is for transferring artifacts;
  edit files with whatever editor you run inside the terminal.

## Why does this exist

Existing tools didn't fit:

- **Warp** — cloud subscription, not self-hosted.
- **Cursor / Windsurf** — IDE-centric, not terminal-first.
- **ttyd / gotty / Wetty** — terminal only, no files, no AI integration.
- **filebrowser** — file management only.
- **Tabby / Wave** — desktop apps, native install per OS.
- **code-server** — VS Code in a browser; great, but VS Code-shaped.

There was no "terminal + file tree + AI dashboard, web-based, self-hosted"
option. So this exists.

## License

MIT. See [LICENSE](LICENSE).
