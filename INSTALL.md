# roost — install & quick start

## What this is

A self-hosted browser workspace: terminal + file tree + AI activity
panel, all in one Go binary. SSH-tunnel it to your dev box, open
`http://localhost:8080` in any browser.

## Validated combos

- **Server**: Linux x86_64 / arm64, macOS arm64 / x86_64
- **Browser**: Chrome on Windows; Chrome / Safari on macOS

Other combos (Firefox, mobile browsers) generally work but aren't on
the daily-driven list — fixes for any rough edges land as I hit them.

## Requirements

- `tmux` ≥ 3.0 on the server (`apt install tmux` / `brew install tmux`).
  roost refuses to start without it.

## Install

```sh
# 1. Extract this tarball
tar -xzf roost-<version>-<os>-<arch>.tar.gz
cd roost-<version>-<os>-<arch>

# 2. Move the binary somewhere on PATH (optional)
sudo install -m 0755 roost /usr/local/bin/roost
# …or just keep it where it is and run ./roost.

# 3. Set a password (writes ~/.config/roost/config.toml)
roost setup
# or non-interactive:
echo 'your-password' | roost setup --password-stdin

# 4. Run
roost serve
# → roost listening on http://127.0.0.1:8080
```

`roost serve` binds to loopback only. From your laptop:

```sh
ssh -L 8080:localhost:8080 user@your-dev-box
# then open http://localhost:8080 in a browser
```

## Run as a service

systemd user unit (Linux) and launchd plist (macOS) recipes live in
the project README — including `loginctl enable-linger` so the systemd
unit survives logout, and the macOS-specific locale env that keeps
tmux's UTF-8 mode working under launchd.

## More

Full docs, screenshots, security policy:
<https://github.com/liamsysmind/roost>

Issues / suggestions:
<https://github.com/liamsysmind/roost/issues>
