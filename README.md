# roost

**A self-hosted browser workspace for terminal-first AI coding.**

Install one binary on your dev box. SSH-tunnel it. Open a browser. You get a
real terminal, a file tree, and a panel that watches your Claude Code
session — all in one page, on every device you own.

- **Terminal that survives disconnects.** Backed by `tmux` under the hood,
  so closing your laptop, switching networks, or restarting the server
  doesn't kill your running build or your in-flight agent.
- **File tree next to the terminal.** Click to preview. Drag-drop or
  Ctrl+V from your OS to upload. Click a file to download.
- **AI session at a glance.** Live model name, context-window usage, and
  every prompt you've sent — click one to scroll the terminal back to it.
- **Multi-session, multi-tab.** Each browser tab is its own named shell.
  Open two tabs on the same session to share the same screen with yourself.
- **No cloud. No subscription. No account.** One process, one user,
  one binary.

> Status: pre-1.0, single-user, designed for SSH-tunnel deployment.
> Pricing/cost tracking is deliberately not built in — API rates shift and
> subscription plans bill differently; only token counts are surfaced.

---

## Quick start

### Requirements

- Linux or macOS host with `tmux` ≥ 3.0 (required at runtime — `roost`
  refuses to start without it: `apt install tmux` / `brew install tmux`).
- A modern browser on whatever you SSH from.
- Go 1.23+ if you're building from source. Prebuilt binaries: see
  [Releases](https://github.com/liamsysmind/roost/releases).

### Install

```bash
git clone https://github.com/liamsysmind/roost && cd roost
make                       # builds ./roost
sudo make install          # installs /usr/local/bin/roost  (optional)
```

Or grab a binary for your target:

```bash
make cross                 # writes dist/roost-<version>-<os>-<arch>
```

### One-time setup

```bash
roost setup
# (prompts for a password; writes ~/.config/roost/config.toml)
```

Or non-interactive:

```bash
echo 'your-password' | roost setup --password-stdin
```

`setup` picks a free port automatically (default 8080, falls back to 8081…
if taken) and writes the chosen address into the config along with a
bcrypt password hash, a session secret, and a hook secret.

### Run

```bash
roost serve
# → roost listening on http://127.0.0.1:8080
```

`roost` binds to loopback only. You reach it from your laptop with an
SSH tunnel:

```bash
ssh -L 8080:localhost:8080 user@your-dev-box
```

Open <http://localhost:8080> in your browser, sign in with the password
you set. Done.

---

## What you get

### Sessions

The home page is a session picker. Type a memorable name (or leave blank
for a random ID) and hit **+ New session** to launch a fresh tmux-backed
shell. Each session is a separate URL like `/s/zephyr-build`, so:

- closing the tab doesn't kill the shell
- coming back to the URL re-attaches you to the same screen
- you can rename or delete sessions from the home list
- two tabs on the same URL get the **same** live view — useful for
  showing the screen to a colleague over a screenshare

### Terminal

A full xterm.js terminal with WebGL rendering and unlimited scrollback
(persisted to a log file on disk, so reattaching after a server restart
still shows the conversation you had this morning).

Keyboard ergonomics that match the OS:

- `Ctrl+C` copies selected text, or sends `SIGINT` if nothing is selected
- `Ctrl+Shift+C` always copies
- `Ctrl+Shift+V` pastes from clipboard into the shell

### File panel

Right side of the page. Two tabs:

- **Files** — browse, preview, upload, download, rename, delete.
  - Click a file to **preview** it inline (images, video, audio, PDF,
    text — auto-detected from MIME type).
  - The tree follows the terminal's `cd` automatically.
  - **Drag-drop** anywhere on the page or **Ctrl+V** from your OS to
    upload. Status line shows the destination path so there's no
    "where did my file go".
  - Click a file row or the ↓ icon to download. Progress bar on big files.
  - Uploads stream straight to disk — no `/tmp` buffering, no memory
    blow-up, no 1 GB cap.
- **AI** — live view of the Claude Code session running in the
  terminal's current directory.
  - Model name, context tokens used / window estimate, message count,
    and token breakdown.
  - List of every prompt you've sent; click one to scroll the terminal
    scrollback back to that point.
  - Auto-refreshes; clears when you `cd` to a directory without a
    Claude project.

### Notifications

Browser push notifications via Server-Sent Events:

```bash
roost hook-info
```

…prints a ready-to-paste snippet for `~/.claude/settings.json`. Once
configured, Claude Code's `Stop` hook fires a notification to your
browser whenever an agent stops waiting for input — your laptop pings,
even if you're in a different tab.

---

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

---

## Multi-user on one machine

`roost` is deliberately single-user per instance. If two people share a
host, they each run their own `roost` on a different port — UNIX UIDs
do the isolation, the binary stays simple.

```
[ shared linux host ]

  alice (UID 1001)                         bob (UID 1002)
   ~/.config/roost/config.toml              ~/.config/roost/config.toml
   ~/.local/share/roost/sessions/           ~/.local/share/roost/sessions/
   roost serve --addr 127.0.0.1:8081        roost serve --addr 127.0.0.1:8082

   from her laptop:                         from his laptop:
   ssh -L 8081:localhost:8081 alice@host    ssh -L 8082:localhost:8082 bob@host
   open http://localhost:8081               open http://localhost:8082
```

Each `roost` runs as its own user, sees only its own home / `tmux` /
`~/.claude/`. No state crosses between them.

---

## Running as a service

### Linux (systemd user unit)

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

---

## Public access via Cloudflare Tunnel

If you don't want to SSH-tunnel every time (especially from phones or
collaborators' machines), front `roost` with a [Cloudflare Tunnel] and
[Cloudflare Access]. You get a public HTTPS URL with zero open ports
on your machine, TLS terminated at the edge, and email/SSO auth in
front of the password.

```
[ your phone / laptop / anywhere ]
            ↓
   https://roost.example.com   (Cloudflare Edge: TLS + Access SSO)
            ↓
       Cloudflare Tunnel       (outbound from your host)
            ↓
         cloudflared           (running alongside roost)
            ↓
       http://127.0.0.1:8080   (roost — never exposed to a public port)
```

### Setup

1. **Keep `roost` on loopback** — no config change needed:

   ```toml
   [server]
   addr = "127.0.0.1:8080"
   ```

2. **Add a Public Hostname to your existing tunnel** (Cloudflare Zero
   Trust → Networks → Tunnels → \[your tunnel\] → Public Hostname → Add):

   | Field    | Value                          |
   |----------|--------------------------------|
   | Subdomain | `roost` (or whatever)         |
   | Domain    | your domain                   |
   | Service   | Type `HTTP`, URL `localhost:8080` |
   | Additional → TLS | check **No TLS Verify** |

3. **Put Cloudflare Access in front** (strongly recommended — without
   it, anyone on the internet who finds your hostname is exposed to
   `roost`'s single password). Zero Trust → Access → Applications →
   Add → Self-hosted:

   - Application: `roost`
   - Domain: `roost.example.com`
   - Policy: Allow, with rule **Emails → include → your address**

   First page load now requires an email OTP or your SSO. roost's
   password becomes a second factor behind that.

### Compatibility notes

| Feature | Through CF Tunnel |
|---------|--------------------|
| Terminal WebSocket / SSE notifications | ✅ tunnels support WS natively |
| HTTPS                              | ✅ Cloudflare provides the cert |
| `Stop`-hook POST to `/api/notify`  | ✅ Hooks hit `127.0.0.1:8080` locally, never via the tunnel |
| Drag-drop / Ctrl+V upload          | ⚠️ Cloudflare's request body limit is **100 MB on Free, 200 MB on Pro, 500 MB on Business**. For larger transfers, fall back to SSH tunnel (`ssh -L 8080:localhost:8080`) which has no such limit. |
| Streaming download of multi-GB files | ✅ usually fine; falls back to a direct `<a download>` link past 2 GB |

[Cloudflare Tunnel]: https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/
[Cloudflare Access]: https://developers.cloudflare.com/cloudflare-one/applications/

---

## What roost isn't

- **Not multi-tenant — and never will be.** Two people = two instances.
  Multi-user accounting belongs in a deployment-layer hub, not in this
  codebase.
- **Not exposed to the internet by default.** Loopback only. Reach it
  via SSH tunnel, Tailscale, ZeroTier, WireGuard — your call.
- **Not an editor.** The file tree is for moving artifacts in and out.
  Edit files with whatever editor you run inside the terminal.
- **Not a Claude Code clone.** It surfaces what Claude Code already
  records on disk; it doesn't run the agent itself.
- **No usage cost in dollars.** API prices shift, subscription plans
  bill differently. Token counts are shown — interpret them with your
  own price sheet.

---

## Why this exists

Existing tools didn't fit:

- **Warp** — cloud subscription, not self-hosted.
- **Cursor / Windsurf** — IDE-centric, not terminal-first.
- **ttyd / gotty / Wetty** — terminal only, no file management,
  no AI awareness.
- **filebrowser** — file management only, no terminal.
- **Tabby / Wave** — desktop apps, native install per OS.
- **code-server** — VS Code in a browser; great, but VS Code-shaped.

`roost` is the missing intersection: terminal + file tree + AI panel,
self-hosted on your own machine, accessed through any browser.

---

## License

[MIT](LICENSE).
