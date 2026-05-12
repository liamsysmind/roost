package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

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
//   - history-limit 0     : tmux scrollback off — roost's own log file is the
//                           authoritative history and is replayed on attach.
//   - escape-time 0       : avoids the 500ms delay after ESC that breaks vim/AI
//                           agents.
//   - default-terminal    : matches what most modern terminals advertise.
//   - destroy-unattached off : keep the session alive even when no client is
//                           attached. This is the whole point.
const roostTmuxConf = `
set-option -g status off
unbind-key -a
set-option -g history-limit 0
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
	m := &Manager{
		cfg:          cfg,
		tmuxConfPath: confPath,
		sessions:     map[string]*Session{},
		stop:         make(chan struct{}),
	}
	go m.gcLoop()
	return m, nil
}

func writeTmuxConf() (string, error) {
	f, err := os.CreateTemp("", "roost-tmux-*.conf")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(roostTmuxConf); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
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
