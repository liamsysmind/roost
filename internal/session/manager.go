package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// tmuxConfBasename is the deterministic filename used for the embedded tmux
// configuration. UID is included so multiple users on the same host (each
// running their own roost per CLAUDE.md) don't collide on file ownership.
func tmuxConfBasename() string {
	return fmt.Sprintf("roost-tmux-uid%d.conf", os.Getuid())
}

// Manager owns the active set of Sessions and GC-s idle ones.
type Manager struct {
	cfg Config

	tmuxConfPath string

	mu       sync.Mutex
	sessions map[string]*Session

	stop chan struct{}
}

// roostTmuxConf is the embedded tmux configuration used for every session.
//
// Why these specific settings:
//   - status off          : hides tmux's status bar so the UI looks like a plain
//                           shell. Users shouldn't have to know tmux is there.
//   - unbind-key -a       : drops every default key binding (including C-b),
//                           so the prefix can never be hit by accident. Keys
//                           flow straight to the shell.
//   - history-limit 50000 : tmux's own per-pane scrollback. We rely on tmux
//                           feeding scrolled-off lines to xterm.js incrementally,
//                           but a non-trivial history-limit is also needed so
//                           that tmux's pane has somewhere to put text before
//                           it's relayed (and so `capture-pane` works for tools
//                           that want pane state).
//   - terminal-overrides smcup@/rmcup@ : strip the alt-screen capabilities so
//                           tmux never sends CSI ?1049h to xterm.js. Without
//                           this, tmux enters the alt buffer on attach, which
//                           has zero scrollback per terminal protocol — wheel
//                           and keyboard scrolling do nothing, AND xterm.js's
//                           fallback converts wheel events into arrow-key
//                           keystrokes (bash receives ↑/↓ and cycles through
//                           command history instead of scrolling).
//
//                           Apps inside tmux that use alt-screen (vim, less,
//                           man) still work as expected: tmux's pane enters
//                           alt-screen internally and renders the app to the
//                           client as normal output. On exit, tmux redraws the
//                           pre-app pane state — so the screen "restores" the
//                           way users expect, AND the app's content lands in
//                           xterm.js's scrollback for later review. Best of
//                           both worlds because tmux acts as a translator.
//
//                           `set -g alternate-screen off` alone is not
//                           sufficient — tmux still emits the alt-screen toggle
//                           without smcup/rmcup overridden.
//   - terminal-overrides indn@/rin@   : strip the parametrised index/reverse-
//                           index capabilities. tmux uses CSI Pn S (Scroll Up
//                           N) and CSI Pn T (Scroll Down N) as a batched-scroll
//                           optimization instead of N individual line feeds.
//                           xterm.js implements those per spec — the scrolled
//                           lines are DROPPED, not pushed into scrollback. So
//                           a bash command that overflows the pane would have
//                           its earlier output appear briefly and then vanish
//                           with no way to scroll back. Removing indn/rin
//                           forces tmux to emit individual line feeds, each of
//                           which properly accumulates in xterm.js scrollback.
//   - escape-time 0       : avoids the 500ms delay after ESC that breaks vim/AI
//                           agents.
//   - default-terminal    : matches what most modern terminals advertise.
//   - destroy-unattached off : keep the session alive even when no client is
//                           attached. This is the whole point.
const roostTmuxConf = `
set-option -g status off
unbind-key -a
set-option -g history-limit 50000
set-option -ga terminal-overrides ',*:smcup@:rmcup@:indn@:rin@'
set-option -sg escape-time 0
set-option -g default-terminal "tmux-256color"
set-option -g destroy-unattached off
`

// NewManager starts a manager and its GC loop. Caller should Shutdown on exit.
// Returns an error if tmux is unavailable; roost requires it for shell
// persistence (without tmux, shells would die when the WS disconnects).
func NewManager(cfg Config) (*Manager, error) {
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = 24 * time.Hour
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, fmt.Errorf("tmux not found in PATH; roost requires tmux for session persistence (apt install tmux / brew install tmux): %w", err)
	}
	confPath, err := writeTmuxConf()
	if err != nil {
		return nil, fmt.Errorf("write tmux conf: %w", err)
	}
	// If a tmux server is already running on the default socket (left over from
	// a previous roost lifetime, or shared with the user's own tmux), source
	// our conf into it so the new option values take effect immediately.
	// Without this, an upgrade that changes terminal-overrides etc. would only
	// affect freshly-spawned tmux servers — existing sessions would keep using
	// the obsolete settings for the rest of their lifetime.
	_ = exec.Command("tmux", "source-file", confPath).Run()
	m := &Manager{
		cfg:          cfg,
		tmuxConfPath: confPath,
		sessions:     map[string]*Session{},
		stop:         make(chan struct{}),
	}
	go m.gcLoop()
	return m, nil
}

// writeTmuxConf writes the embedded tmux configuration to a deterministic
// path in $TMPDIR and returns it. The path is stable across roost restarts so
// non-graceful exits (SIGKILL, crash, `make dev`) don't leak a fresh file each
// time — every invocation reuses or overwrites the same one. As a side effect
// it also sweeps legacy random-named "roost-tmux-*.conf" files left by older
// versions that used os.CreateTemp. We only remove files we own to stay safe
// on shared hosts.
func writeTmuxConf() (string, error) {
	dir := os.TempDir()
	path := filepath.Join(dir, tmuxConfBasename())
	if err := os.WriteFile(path, []byte(roostTmuxConf), 0o600); err != nil {
		return "", err
	}
	sweepLegacyTmuxConfs(dir, path)
	return path, nil
}

// sweepLegacyTmuxConfs removes "roost-tmux-*.conf" files owned by the current
// user except for `keep`. Errors are swallowed: cleanup is best-effort and
// must never prevent startup.
func sweepLegacyTmuxConfs(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	uid := os.Getuid()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "roost-tmux-") || !strings.HasSuffix(name, ".conf") {
			continue
		}
		full := filepath.Join(dir, name)
		if full == keep {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != uid {
			continue
		}
		_ = os.Remove(full)
	}
}

// tmuxSessionExists reports whether tmux already tracks the given session
// (e.g. left over from a previous roost run).
func (m *Manager) tmuxSessionExists(id string) bool {
	return exec.Command("tmux", "-f", m.tmuxConfPath, "has-session", "-t", "="+id).Run() == nil
}

// killTmuxSession asks tmux to terminate a session. Idempotent.
func (m *Manager) killTmuxSession(id string) {
	_ = exec.Command("tmux", "-f", m.tmuxConfPath, "kill-session", "-t", "="+id).Run()
}

// PaneInfo asks tmux for the active pane's cwd, foreground command name, and
// the AI agent currently running anywhere in the pane's process tree (Claude
// Code / Codex, or "" for plain shell). Works without shell integration: tmux
// derives cwd and the foreground command from the foreground process's
// /proc/<pid>; agent detection walks ps output from the pane's shell PID. We
// avoid the "=" exact-match prefix because display-message returns empty for
// it (unlike has-session / kill-session). The -u flag is required: without a
// UTF-8 locale tmux rewrites the 0x1f delimiter to '_', leaving the three
// fields glued together (so cwd becomes "/tmp/x_bash_42" and the split fails).
func (m *Manager) PaneInfo(id string) (cwd, cmd, app string, err error) {
	if e := ValidateID(id); e != nil {
		return "", "", "", e
	}
	out, e := exec.Command("tmux",
		"-u",
		"-f", m.tmuxConfPath,
		"display-message", "-t", id,
		"-p", "#{pane_current_path}\x1f#{pane_current_command}\x1f#{pane_pid}").Output()
	if e != nil {
		return "", "", "", e
	}
	parts := strings.SplitN(strings.TrimRight(string(out), "\n"), "\x1f", 3)
	cwd = parts[0]
	if len(parts) >= 2 {
		cmd = strings.TrimSpace(parts[1])
	}
	if len(parts) == 3 {
		if pid, perr := strconv.Atoi(strings.TrimSpace(parts[2])); perr == nil {
			app = classifyPaneTree(pid)
		}
	}
	return cwd, cmd, app, nil
}

// Cwd is a thin wrapper around PaneInfo for callers that only need the cwd
// (e.g. the AI handler resolving project paths).
func (m *Manager) Cwd(id string) (string, error) {
	cwd, _, _, err := m.PaneInfo(id)
	return cwd, err
}

// classifyPaneTree walks the descendants of panePid via `ps` and returns
// "claude" / "codex" if a matching process is found anywhere in the tree, or
// "" otherwise. We use ps (BSD + GNU compatible flags) instead of /proc so
// the same code path works on Linux and macOS without build tags.
//
// Matching logic distinguishes:
//   - direct binary: process basename is "claude" or "codex"
//   - node launcher: process basename is "node" and the command path contains
//     a path segment like "/claude/", "/claude-", "/claude.js", or trailing
//     "/claude" (same for codex). A plain `node script.js` won't match.
func classifyPaneTree(panePid int) string {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,ppid=,command=").Output()
	if err != nil {
		return ""
	}
	type rec struct {
		ppid int
		cmd  string
	}
	procs := make(map[int]rec, 256)
	children := make(map[int][]int, 256)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, e1 := strconv.Atoi(fields[0])
		ppid, e2 := strconv.Atoi(fields[1])
		if e1 != nil || e2 != nil {
			continue
		}
		cmd := strings.Join(fields[2:], " ")
		procs[pid] = rec{ppid: ppid, cmd: cmd}
		children[ppid] = append(children[ppid], pid)
	}
	queue := []int{panePid}
	seen := map[int]bool{panePid: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range children[cur] {
			if seen[c] {
				continue
			}
			seen[c] = true
			queue = append(queue, c)
			if app := classifyCommand(procs[c].cmd); app != "" {
				return app
			}
		}
	}
	return ""
}

func classifyCommand(cmd string) string {
	space := strings.IndexByte(cmd, ' ')
	head := cmd
	if space > 0 {
		head = cmd[:space]
	}
	switch filepath.Base(head) {
	case "claude":
		return "claude"
	case "codex":
		return "codex"
	case "node":
		if containsExecSegment(cmd, "claude") {
			return "claude"
		}
		if containsExecSegment(cmd, "codex") {
			return "codex"
		}
	}
	return ""
}

// containsExecSegment looks for `name` as a path segment or executable suffix
// in a command line. Matches "/name/", "/name-", "/name.js", or "/name" at
// end of a token — enough to identify a node-launched CLI without matching
// arbitrary mentions of the word elsewhere in the args.
func containsExecSegment(cmd, name string) bool {
	if strings.Contains(cmd, "/"+name+"/") ||
		strings.Contains(cmd, "/"+name+"-") ||
		strings.Contains(cmd, "/"+name+".js") {
		return true
	}
	// "/name" at end of any space-delimited token
	for _, tok := range strings.Fields(cmd) {
		if strings.HasSuffix(tok, "/"+name) {
			return true
		}
	}
	return false
}

// GetOrCreate returns the named session, attaching to the tmux session of the
// same name (creating it if it doesn't exist).
func (m *Manager) GetOrCreate(id string) (*Session, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok && !s.IsClosed() {
		return s, nil
	}
	if _, ok := m.sessions[id]; ok {
		// Stale closed entry — drop it (but keep the on-disk log).
		delete(m.sessions, id)
	}
	s, err := newSession(id, m.cfg, m.tmuxConfPath, m.tmuxSessionExists(id))
	if err != nil {
		return nil, err
	}
	m.sessions[id] = s
	return s, nil
}

// Get returns an existing session, or nil if not present.
func (m *Manager) Get(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

// List returns metadata about all known sessions: live in-memory ones plus
// any orphan log files on disk (e.g. sessions from a previous server run).
// Clicking an orphan re-opens a fresh PTY and resumes scrollback from the
// existing log.
func (m *Manager) List() []Info {
	m.mu.Lock()
	out := make([]Info, 0, len(m.sessions))
	seen := make(map[string]bool, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, Info{
			ID:           s.ID,
			Clients:      s.ClientCount(),
			Closed:       s.IsClosed(),
			LastUsed:     s.LastUsed(),
			LogSizeBytes: s.log.Size(),
		})
		seen[s.ID] = true
	}
	m.mu.Unlock()

	entries, err := os.ReadDir(m.cfg.LogDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}
		id := strings.TrimSuffix(name, ".log")
		if seen[id] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Info{
			ID:           id,
			Clients:      0,
			Closed:       false,
			LastUsed:     info.ModTime(),
			LogSizeBytes: info.Size(),
		})
	}
	return out
}

// Info is a snapshot of a session for the /api/sessions endpoint (W3+).
type Info struct {
	ID           string    `json:"id"`
	Clients      int       `json:"clients"`
	Closed       bool      `json:"closed"`
	LastUsed     time.Time `json:"last_used"`
	LogSizeBytes int64     `json:"log_size_bytes"`
}

// ValidateID rejects names that could escape the session log directory or
// otherwise misbehave on disk. The allowed character class matches what
// the home page UI lets users type.
func ValidateID(id string) error {
	if id == "" {
		return errors.New("session name must be non-empty")
	}
	if len(id) > 128 {
		return errors.New("session name too long (max 128 chars)")
	}
	if id == "." || id == ".." {
		return errors.New("invalid session name")
	}
	for _, r := range id {
		ok := r == '-' || r == '_' || r == '.' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("session name contains invalid character %q (allowed: A-Z a-z 0-9 . _ -)", r)
		}
	}
	return nil
}

// Rename changes the session's identifier and migrates its log file.
// Works on both live (in-memory) sessions and orphan log files on disk.
// Live clients keep their existing WebSocket but new connects must use
// the new name.
func (m *Manager) Rename(oldID, newID string) error {
	if err := ValidateID(newID); err != nil {
		return err
	}
	if oldID == newID {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[newID]; exists {
		return fmt.Errorf("name %q already in use", newID)
	}
	newLog := filepath.Join(m.cfg.LogDir, newID+".log")
	if _, err := os.Stat(newLog); err == nil {
		return fmt.Errorf("name %q already in use (log file exists)", newID)
	}

	if s, ok := m.sessions[oldID]; ok {
		// Live session — rename log and re-key the map.
		if err := os.Rename(s.logPath, newLog); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rename log: %w", err)
		}
		s.ID = newID
		s.logPath = newLog
		delete(m.sessions, oldID)
		m.sessions[newID] = s
		return nil
	}

	// Orphan log on disk — just rename the file.
	oldLog := filepath.Join(m.cfg.LogDir, oldID+".log")
	if err := os.Rename(oldLog, newLog); err != nil {
		if os.IsNotExist(err) {
			return errors.New("session not found")
		}
		return fmt.Errorf("rename log: %w", err)
	}
	return nil
}

// Delete closes a session and removes its log file. Also kills the underlying
// tmux session so the shell process actually terminates.
func (m *Manager) Delete(id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	m.killTmuxSession(id)
	if ok {
		s.Close()
		return s.RemoveLog()
	}
	// No live session — still try to remove a stale log file if any.
	stale := filepath.Join(m.cfg.LogDir, id+".log")
	if err := os.Remove(stale); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Shutdown disconnects every active client and closes our PTY wrappers.
// It deliberately leaves the underlying tmux sessions running — that is the
// whole point of using tmux: shells survive roost restarts.
func (m *Manager) Shutdown() {
	close(m.stop)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		s.Close()
	}
	m.sessions = nil
	if m.tmuxConfPath != "" {
		_ = os.Remove(m.tmuxConfPath)
	}
}

func (m *Manager) gcLoop() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			m.gcOnce()
		case <-m.stop:
			return
		}
	}
}

func (m *Manager) gcOnce() {
	cutoff := time.Now().Add(-m.cfg.IdleTTL)
	m.mu.Lock()
	var victims []*Session
	for id, s := range m.sessions {
		if s.IsClosed() {
			victims = append(victims, s)
			delete(m.sessions, id)
			continue
		}
		if s.ClientCount() == 0 && s.LastUsed().Before(cutoff) {
			victims = append(victims, s)
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()
	for _, s := range victims {
		s.Close()
	}
}
