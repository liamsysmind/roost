package session

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"
)

// Session owns one shell + PTY pair and fans its output out to attached clients.
type Session struct {
	ID      string
	logPath string

	cmd *exec.Cmd
	tty *os.File
	log *Log

	mu       sync.Mutex
	clients  map[*Client]struct{}
	closedAt atomic.Int64 // unix nano; 0 = live
	lastUsed atomic.Int64 // unix nano

	cfg  Config
	done chan struct{}
}

// Config controls session creation defaults.
type Config struct {
	LogDir      string        // directory for per-session log files
	ReplayBytes int64         // tail bytes sent on attach; <=0 means full log
	IdleTTL     time.Duration // close a session this long after the last client detaches
	Shell       string        // override $SHELL
}

func newSession(id string, cfg Config) (*Session, error) {
	if err := os.MkdirAll(cfg.LogDir, 0o700); err != nil {
		return nil, err
	}
	logPath := filepath.Join(cfg.LogDir, id+".log")
	lg, err := openLog(logPath)
	if err != nil {
		return nil, err
	}

	// If we're resuming an existing log (e.g. the previous shell exited
	// or the server restarted), write a visible banner so the user can
	// tell where the old output ends and the fresh shell begins.
	// Without this, replaying the log shows the old shell's last prompt
	// immediately next to the new shell's first prompt — confusing.
	if lg.Size() > 0 {
		banner := []byte(fmt.Sprintf("\r\n\x1b[2;33m── new shell %s ──\x1b[0m\r\n",
			time.Now().Format("2006-01-02 15:04:05")))
		_, _ = lg.Write(banner)
	}

	shell := cfg.Shell
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
	}
	cmd := exec.Command(shell, "-l")
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"ROOST=1",
		"ROOST_SESSION="+id,
	)
	tty, err := pty.Start(cmd)
	if err != nil {
		_ = lg.Close()
		return nil, err
	}

	s := &Session{
		ID:      id,
		logPath: logPath,
		cmd:     cmd,
		tty:     tty,
		log:     lg,
		clients: map[*Client]struct{}{},
		cfg:     cfg,
		done:    make(chan struct{}),
	}
	s.lastUsed.Store(time.Now().UnixNano())
	go s.readLoop()
	return s, nil
}

// readLoop pumps PTY output → log → attached clients.
func (s *Session) readLoop() {
	defer close(s.done)
	buf := make([]byte, 8192)
	for {
		n, err := s.tty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.mu.Lock()
			if _, werr := s.log.Write(chunk); werr != nil {
				log.Printf("session %s: log write: %v", s.ID, werr)
			}
			for c := range s.clients {
				c.send(chunk)
			}
			s.mu.Unlock()
			s.lastUsed.Store(time.Now().UnixNano())
		}
		if err != nil {
			s.markClosed()
			return
		}
	}
}

// Attach registers a client and replays the log tail before they go live.
// Holding the session mutex around snapshot+register ensures no live byte
// is lost between replay and broadcast.
func (s *Session) Attach(c *Client) error {
	if s.IsClosed() {
		return errors.New("session is closed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, err := s.log.Snapshot(s.cfg.ReplayBytes)
	if err != nil {
		return err
	}
	if len(snap) > 0 {
		c.send(snap)
	}
	s.clients[c] = struct{}{}
	return nil
}

func (s *Session) Detach(c *Client) {
	s.mu.Lock()
	delete(s.clients, c)
	n := len(s.clients)
	s.mu.Unlock()
	if n == 0 {
		s.lastUsed.Store(time.Now().UnixNano())
	}
}

// Input writes bytes from a client into the PTY.
func (s *Session) Input(p []byte) error {
	if s.IsClosed() {
		return errors.New("session is closed")
	}
	_, err := s.tty.Write(p)
	if err == nil {
		s.lastUsed.Store(time.Now().UnixNano())
	}
	return err
}

// Resize forwards a window-size change to the PTY.
func (s *Session) Resize(rows, cols uint16) error {
	if s.IsClosed() {
		return nil
	}
	return pty.Setsize(s.tty, &pty.Winsize{Rows: rows, Cols: cols})
}

// ClientCount returns how many clients are currently attached.
func (s *Session) ClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// IsClosed reports whether the underlying shell has exited.
func (s *Session) IsClosed() bool {
	return s.closedAt.Load() != 0
}

// LastUsed returns the last time bytes flowed through this session.
func (s *Session) LastUsed() time.Time {
	return time.Unix(0, s.lastUsed.Load())
}

func (s *Session) markClosed() {
	if !s.closedAt.CompareAndSwap(0, time.Now().UnixNano()) {
		return
	}
	s.mu.Lock()
	clients := make([]*Client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	for _, c := range clients {
		c.closeWith(1000, "shell exited")
	}
}

// Close terminates the shell and releases resources. Idempotent.
func (s *Session) Close() {
	s.markClosed()
	if s.tty != nil {
		_ = s.tty.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	<-s.done
	_ = s.log.Close()
}

// RemoveLog deletes the on-disk log file. Call only after Close.
func (s *Session) RemoveLog() error {
	if s.logPath == "" {
		return nil
	}
	return os.Remove(s.logPath)
}
