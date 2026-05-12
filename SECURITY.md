# Security policy

## Reporting a vulnerability

Please **don't open a public GitHub issue** for security problems.
Use one of:

- GitHub's private vulnerability reporting:
  <https://github.com/liamsysmind/roost/security/advisories/new>
- Email: <william.lee3438@gmail.com>

You'll get an acknowledgement within a few days and we'll coordinate
on a fix and disclosure timeline.

## Threat model

roost is **single-user, self-hosted, designed for SSH-tunnel /
Tailscale / Cloudflare-Tunnel deployment**. By default it binds to
`127.0.0.1`. What counts as a vulnerability is shaped by that.

### In scope

- Path traversal in `/api/fs/*` — any request that reads or writes
  outside the configured `[fs] root`
- Session-cookie hijacking, session-fixation, or replay
- Authentication bypasses (reaching protected routes without a
  valid cookie / hook secret)
- Privilege escalation outside the running roost user's normal
  shell access
- Hook-secret leakage that lets an unrelated local process push
  notifications

### Out of scope

- Anything assuming the binary is exposed to the public internet
  **without** a fronting service (TLS proxy, Tailscale, SSH tunnel,
  Cloudflare Tunnel). roost ships no TLS, no rate limiting, no
  brute-force protection — those belong to the deployment layer.
- Multi-tenant isolation: there is no multi-tenancy, and never will be.
  Two users on the same box = two independent `roost` processes.
- DoS from legitimate workloads (multi-GB uploads, days-long sessions).
- Issues that require the attacker to already have a shell as the
  same UNIX user that runs roost (at that point they own everything
  anyway).

## Updates

Fixes ship as new git tags. There is no LTS branch — please run a
recent `main`.
