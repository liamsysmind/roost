package session

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A peer that stops answering pings has to be torn out of the broadcast set
// within pongWait. Before the read deadline existed, ReadMessage blocked
// forever on a connection that had died at the transport layer — a slept
// phone, a Wi-Fi handover — and the client stayed attached, still being
// queued output, until the OS TCP keepalive gave up hours later.
func TestSilentPeerIsDetachedWithinPongWait(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	// Shrink the liveness window so the test finishes in under a second.
	// Restored so the rest of the package sees the real values.
	oldPing, oldPong := pingInterval, pongWait
	pingInterval, pongWait = 50*time.Millisecond, 200*time.Millisecond
	defer func() { pingInterval, pongWait = oldPing, oldPong }()

	m, err := NewManager(Config{LogDir: t.TempDir(), IdleTTL: time.Hour})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Shutdown()

	const id = "roost-livenesstest-xyz"
	defer m.Delete(id)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/{id}", (&Handler{Manager: m}).Serve)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// A raw dialer, then never read again: gorilla only answers a ping while
	// the application is inside ReadMessage, so a client that stops reading is
	// exactly the silent peer this guards against.
	conn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/"+id, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	attached := func() int {
		for _, info := range m.List() {
			if info.ID == id {
				return info.Clients
			}
		}
		return -1
	}

	// It has to be attached first, or the rest proves nothing.
	deadline := time.Now().Add(2 * time.Second)
	for attached() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("client never attached (count %d)", attached())
		}
		time.Sleep(5 * time.Millisecond)
	}

	start := time.Now()
	deadline = time.Now().Add(5 * time.Second)
	for attached() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("silent peer still attached after %v; read deadline did not fire",
				time.Since(start))
		}
		time.Sleep(5 * time.Millisecond)
	}
	elapsed := time.Since(start)

	// Detected on the deadline, not before it: a healthy connection that just
	// happens to be quiet must not be dropped early.
	if elapsed < pongWait/2 {
		t.Errorf("detached after %v, sooner than half of pongWait %v", elapsed, pongWait)
	}
	t.Logf("silent peer detached after %v (pongWait %v)", elapsed, pongWait)
}

// The other half: a connection that is merely quiet — nobody typing, nothing
// printing — must survive well past pongWait. gorilla answers the server's
// pings from inside ReadMessage, so a client that keeps reading is a healthy
// one, and the deadline must keep being pushed back.
func TestQuietButLivePeerStaysAttached(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	oldPing, oldPong := pingInterval, pongWait
	pingInterval, pongWait = 50*time.Millisecond, 200*time.Millisecond
	defer func() { pingInterval, pongWait = oldPing, oldPong }()

	m, err := NewManager(Config{LogDir: t.TempDir(), IdleTTL: time.Hour})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Shutdown()

	const id = "roost-livenesstest-quiet"
	defer m.Delete(id)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/{id}", (&Handler{Manager: m}).Serve)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/"+id, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Keep reading, and send nothing: pongs are the only traffic from this
	// side, which is exactly a browser tab sitting idle.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	attached := func() int {
		for _, info := range m.List() {
			if info.ID == id {
				return info.Clients
			}
		}
		return -1
	}

	deadline := time.Now().Add(2 * time.Second)
	for attached() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("client never attached")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Four pongWaits of silence. A deadline that is not being refreshed would
	// have fired several times over by now.
	time.Sleep(4 * pongWait)
	if n := attached(); n != 1 {
		t.Fatalf("quiet but live peer was dropped: attached=%d after %v", n, 4*pongWait)
	}
	select {
	case <-done:
		t.Fatal("reader goroutine ended; the server closed a healthy connection")
	default:
	}
}
