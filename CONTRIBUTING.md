# Contributing to roost

Thanks for the interest. roost is small and opinionated; this guide is
honest about what fits.

## What's likely to land

- Bug fixes (please include a clear reproduction)
- Cross-platform compatibility, especially **macOS** — barely tested
- New AI-agent integrations (Codex, Aider, Cody, …): anything the
  AI tab currently only does for Claude Code
- Hardening of existing features (input validation, better error UX)
- Documentation improvements, screenshots, demo GIFs

## What's likely to be declined

- **Multi-tenant features** (user accounts, RBAC, per-user data
  partitions inside one `roost` process). See `CLAUDE.md` →
  "Design philosophy — non-negotiable".
- **A bundler / build pipeline** (Svelte, Vite, …). Frontend stays
  vanilla JS until a feature genuinely doesn't fit.
- **USD cost estimation.** Removed deliberately — API prices shift
  and subscription plans don't match raw rates. Tokens are honest;
  dollars aren't.
- **A built-in editor.** Edit files with whatever editor you run
  inside the terminal.

When in doubt, open an issue describing the change before writing it.

## Dev loop

```bash
git clone https://github.com/liamsysmind/roost && cd roost
make             # builds ./roost
./roost setup    # one-time password + config
./roost serve    # http://127.0.0.1:8080
```

Frontend changes: rebuild the binary (`make`) and refresh the browser.
Assets are embedded via `go:embed`, so there's no separate static
server to restart.

Cross-compile to every supported target:

```bash
make cross       # writes dist/roost-<version>-<os>-<arch>
```

## Code conventions

- **Go**: standard formatting (`go fmt ./...`), `go vet ./...` clean.
  Errors wrap with `%w`. HTTP handlers return clear `http.Error`
  bodies; never `panic`.
- **Routes**: each new HTTP route goes through `setupRoutes` and
  passes the auth middleware. Public paths are listed explicitly in
  `isPublicPath`.
- **Filesystem**: any new code that touches disk paths must use the
  `internal/fs` API's `resolve` containment check (path-traversal
  protection lives there).
- **Frontend**: vanilla JS, one file per concern (`fs.js`, `ai.js`,
  `app.js`, `notify.js`, …). To add a new file, drop it in
  `internal/server/web/`, add a route in `server.go` (`mux.HandleFunc("GET /foo.js", s.handleStatic)`),
  and reference it from the HTML.
- Long context: see `CLAUDE.md` for architecture, design decisions,
  and the reasons behind specific quirks (e.g. why we strip CSI
  query sequences from replay buffers).

## Commits

- Imperative subject, ≤ 72 chars
- Body wrapped at 72 chars
- Explain **why**, not just **what** — the diff already shows what

## Pull requests

- Run `make` locally; it must build clean.
- For a backend change, include a smoke-test recipe (curl invocation
  or a tiny Go client) in the PR description.
- For a frontend change, paste a short before/after note or a
  screenshot. (There's no automated visual-diff yet.)
- If the change touches `CLAUDE.md`'s "non-negotiable" sections,
  please discuss in an issue first.

## License

By contributing, you agree that your contribution will be licensed
under the [MIT License](LICENSE) — same as the rest of the project.
