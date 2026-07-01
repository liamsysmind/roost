package session

import (
	"os/exec"
	"testing"
	"time"
)

// TestLastUsedIgnoresOutput verifies the home-page "last used" semantics:
// program output must NOT advance LastUsed (so animated TUIs don't pin every
// busy session to "now"), while keystroke input must; and LastActivity (the
// GC keepalive signal) must still advance on output.
func TestLastUsedIgnoresOutput(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	m, err := NewManager(Config{LogDir: t.TempDir(), IdleTTL: time.Hour})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Shutdown()

	const id = "roost-lastusedtest-xyz"
	s, err := m.GetOrCreate(id)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	defer m.Delete(id)

	// Let the shell settle (initial prompt and any startup output).
	time.Sleep(400 * time.Millisecond)

	usedBefore := s.LastUsed()
	actBefore := s.LastActivity()

	// Produce continuous PTY output WITHOUT going through Session.Input.
	// send-keys injects pane-side, so roost only ever sees it as output in
	// readLoop — exactly like an animated TUI redrawing itself.
	if err := exec.Command("tmux", "-f", m.tmuxConfPath, "send-keys", "-t", id,
		"for i in 1 2 3 4 5 6 7 8 9 10; do echo tick; sleep 0.05; done", "Enter").Run(); err != nil {
		t.Fatalf("send-keys: %v", err)
	}
	time.Sleep(800 * time.Millisecond)

	if !s.LastUsed().Equal(usedBefore) {
		t.Errorf("LastUsed advanced on output alone: before=%v after=%v", usedBefore, s.LastUsed())
	}
	if !s.LastActivity().After(actBefore) {
		t.Errorf("LastActivity did not advance on output: before=%v after=%v", actBefore, s.LastActivity())
	}

	// Actual user input must advance LastUsed.
	if err := s.Input([]byte("\n")); err != nil {
		t.Fatalf("Input: %v", err)
	}
	if !s.LastUsed().After(usedBefore) {
		t.Errorf("LastUsed did not advance on input: before=%v after=%v", usedBefore, s.LastUsed())
	}
}
