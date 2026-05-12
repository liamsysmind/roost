package session

import (
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

// List returns metadata about all currently-tracked sessions.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, Info{
			ID:          s.ID,
			Clients:     s.ClientCount(),
			Closed:      s.IsClosed(),
			LastUsed:    s.LastUsed(),
			LogSizeBytes: s.log.Size(),
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
