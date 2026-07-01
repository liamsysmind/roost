package session

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	mu         sync.Mutex
	clients    map[*Client]struct{}
	closedAt   atomic.Int64 // unix nano; 0 = live
	lastInput  atomic.Int64 // unix nano; last user interaction (input/attach/detach)
	lastOutput atomic.Int64 // unix nano; last PTY output byte

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

func newSession(id string, cfg Config, tmuxConfPath string, tmuxAlreadyExists bool) (*Session, error) {
	if err := os.MkdirAll(cfg.LogDir, 0o700); err != nil {
		return nil, err
	}
	logPath := filepath.Join(cfg.LogDir, id+".log")
	lg, err := openLog(logPath)
	if err != nil {
		return nil, err
	}

	// Write a banner only when a brand-new tmux session is about to spawn
	// a fresh shell into an existing log. If we're attaching to a live tmux
	// session, the screen is just being redrawn — that's true continuity,
	// no banner needed.
	if !tmuxAlreadyExists && lg.Size() > 0 {
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

	// New sessions start in $HOME regardless of where roost was launched.
	// -c is ignored on attach (the existing pane keeps its own cwd), so
	// resuming an old session doesn't get reset.
	startDir, err := os.UserHomeDir()
	if err != nil || startDir == "" {
		startDir = "/"
	}

	// Spawn tmux. -A on new-session means "attach if exists, create otherwise".
	// -u forces UTF-8 mode regardless of locale detection, so multibyte input
	// survives even when LANG/LC_CTYPE is unset (e.g. launchd-spawned daemon).
	// We pass the shell explicitly so a brand-new tmux session uses our chosen
	// login shell instead of whatever tmux's default-shell points at.
	cmd := exec.Command("tmux",
		"-u",
		"-f", tmuxConfPath,
		"new-session", "-A",
		"-s", id,
		"-c", startDir,
		"-x", "200", "-y", "50",
		"--", shell, "-l",
	)
	// Strip TMUX from the environment so tmux doesn't refuse to start nested
	// or print a "sessions should be nested" warning. Also ensure the child
	// shell has a UTF-8 locale: when roost is launched by launchd / systemd
	// neither LANG nor LC_* is inherited, leaving readline in POSIX mode where
	// it mangles every byte ≥0x80 — so CJK input round-trips back as garbage.
	hasUTF8Locale := false
	env := make([]string, 0, len(os.Environ())+4)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_PANE=") {
			continue
		}
		if (strings.HasPrefix(e, "LANG=") || strings.HasPrefix(e, "LC_ALL=") || strings.HasPrefix(e, "LC_CTYPE=")) &&
			strings.Contains(strings.ToLower(e), "utf") {
			hasUTF8Locale = true
		}
		env = append(env, e)
	}
	env = append(env,
		"TERM=xterm-256color",
		"ROOST=1",
		"ROOST_SESSION="+id,
	)
	if !hasUTF8Locale {
		env = append(env, "LANG=en_US.UTF-8")
	}
	cmd.Env = env

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
	now := time.Now().UnixNano()
	s.lastInput.Store(now)
	s.lastOutput.Store(now)
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
			s.lastOutput.Store(time.Now().UnixNano())
		}
		if err != nil {
			s.markClosed()
			return
		}
	}
}

// Attach registers a client and stages the log tail for replay. Holding
// the session mutex around snapshot+register ensures no live byte slips
// between the snapshot and the moment broadcast starts seeing this client.
// The replay buffer itself is streamed out by the client's WriteLoop in
// small chunks so xterm.js can render progressively (see drainReplay) —
// large scrollback no longer freezes the browser tab on attach.
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
		c.queueReplay(snap)
	}
	s.clients[c] = struct{}{}
	return nil
}

func (s *Session) Detach(c *Client) {
	s.mu.Lock()
	delete(s.clients, c)
	s.mu.Unlock()
}

// Input writes bytes from a client into the PTY.
func (s *Session) Input(p []byte) error {
	if s.IsClosed() {
		return errors.New("session is closed")
	}
	_, err := s.tty.Write(p)
	if err == nil {
		s.lastInput.Store(time.Now().UnixNano())
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

// LastUsed returns the last time the user typed into this session (keystroke
// input). It deliberately ignores program output AND attach/detach: an
// animated TUI (claude/codex spinners, token counters, watch/top) emits bytes
// continuously, which would otherwise pin every busy session to "now" and
// collapse the home page's last-used ordering. Merely opening a tab to watch
// output doesn't count as use — only actually working in it does.
func (s *Session) LastUsed() time.Time {
	return time.Unix(0, s.lastInput.Load())
}

// LastActivity returns the more recent of last input and last output. The GC
// uses this (not LastUsed) so a detached-but-busy session — a long build or an
// agent running with no browser tab attached — isn't reaped while it's still
// producing output.
func (s *Session) LastActivity() time.Time {
	in, out := s.lastInput.Load(), s.lastOutput.Load()
	if out > in {
		return time.Unix(0, out)
	}
	return time.Unix(0, in)
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
