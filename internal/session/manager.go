package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager owns the active set of Sessions and GC-s idle ones.
type Manager struct {
	cfg Config

	mu       sync.Mutex
	sessions map[string]*Session

	stop chan struct{}
}

// NewManager starts a manager and its GC loop. Caller should Shutdown on exit.
func NewManager(cfg Config) *Manager {
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = 24 * time.Hour
	}
	m := &Manager{
		cfg:      cfg,
		sessions: map[string]*Session{},
		stop:     make(chan struct{}),
	}
	go m.gcLoop()
	return m
}

// GetOrCreate returns the named session, spawning a new shell if it doesn't
// exist yet (or has already closed).
func (m *Manager) GetOrCreate(id string) (*Session, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok && !s.IsClosed() {
		return s, nil
	}
	if s, ok := m.sessions[id]; ok {
		// closed — drop the stale entry before recreating
		delete(m.sessions, id)
		_ = s.RemoveLog()
	}
	s, err := newSession(id, m.cfg)
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

// Delete closes a session and removes its log file.
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

// Shutdown closes all sessions. After Shutdown the manager is unusable.
func (m *Manager) Shutdown() {
	close(m.stop)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		s.Close()
	}
	m.sessions = nil
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
