package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// logSession is a Session with a real log and no PTY, so tests can feed
// output through Session.output exactly as the read loop does.
func logSession(t *testing.T, segMax int64) *Session {
	t.Helper()
	l, err := openLog(filepath.Join(t.TempDir(), "s.log"), segMax)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return &Session{ID: "t", log: l, clients: map[*Client]struct{}{},
		cfg: Config{ReplayBytes: 1 << 20}}
}

// serve mirrors Handler.Serve without the Manager, which would need tmux.
func serve(s *Session) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		c := NewClient(conn)
		defer c.Close()
		from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
		if err := s.Attach(c, r.URL.Query().Get("epoch"), from); err != nil {
			return
		}
		defer s.Detach(c)
		go c.WriteLoop()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
}

// tab is the browser side of the protocol: it tracks epoch and position the
// way app.js does and collects catch-up and live bytes separately.
type tab struct {
	t       *testing.T
	conn    *websocket.Conn
	epoch   string
	reset   bool
	pos     int64 // -1 until the "live" frame
	catchup []byte
	live    []byte
}

func dial(t *testing.T, srv *httptest.Server, query string) *tab {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/" + query
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &tab{t: t, conn: conn, pos: -1}
}

// readUntil consumes frames until done reports true.
func (b *tab) readUntil(done func() bool) {
	b.t.Helper()
	_ = b.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for !done() {
		mt, data, err := b.conn.ReadMessage()
		if err != nil {
			b.t.Fatalf("read: %v (live %d bytes so far)", err, len(b.live))
		}
		if mt == websocket.TextMessage {
			var m control
			if err := json.Unmarshal(data, &m); err != nil {
				b.t.Fatalf("text frame %q is not a control frame", data)
			}
			switch m.Kind {
			case "start":
				b.epoch, b.reset = m.Epoch, m.Reset
			case "live":
				b.pos = m.Pos
			}
			continue
		}
		if b.pos < 0 {
			b.catchup = append(b.catchup, data...)
		} else {
			b.live = append(b.live, data...)
			b.pos += int64(len(data))
		}
	}
}

func (b *tab) isLive() bool { return b.pos >= 0 }

// The defect this replaces: each client had a 64-chunk queue and anything
// that did not fit was discarded, so a burst of output to a tab that was not
// reading lost line feeds — lines that never reached scrollback. Here a tab
// stops reading while 4 MB is produced, eight times the old queue, and must
// still receive every byte in order.
func TestSlowReaderLosesNothing(t *testing.T) {
	s := logSession(t, 64<<20)
	srv := serve(s)
	defer srv.Close()

	b := dial(t, srv, "")
	b.readUntil(b.isLive)

	var want bytes.Buffer
	start := time.Now()
	for i := 0; want.Len() < 4<<20; i++ {
		line := []byte(fmt.Sprintf("line %07d %s\r\n", i, strings.Repeat("x", 60)))
		want.Write(line)
		s.output(line)
	}
	// The producer must not have waited on the tab that is not reading.
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("producing 4 MB took %s; output is blocking on the client", d)
	}

	// Prove the tab really fell behind: the old queue would have been
	// overflowing at this point, not merely buffered by the socket.
	s.mu.Lock()
	var lag int64
	for c := range s.clients {
		lag = c.Lag()
	}
	s.mu.Unlock()
	if lag < 512<<10 {
		t.Fatalf("client only %d bytes behind; the test is not exercising a backlog", lag)
	}

	b.readUntil(func() bool { return len(b.live) >= want.Len() })
	if !bytes.Equal(b.live, want.Bytes()) {
		t.Fatalf("live stream differs from output: got %d bytes, want %d", len(b.live), want.Len())
	}
}

// A reconnect that names its position gets exactly the bytes produced while
// it was away — not the tail again, and not a reset that wipes the screen.
func TestReconnectResumesFromPosition(t *testing.T) {
	s := logSession(t, 64<<20)
	s.output([]byte("history\r\n"))
	srv := serve(s)
	defer srv.Close()

	first := dial(t, srv, "")
	first.readUntil(first.isLive)
	if !first.reset || string(first.catchup) != "history\r\n" {
		t.Fatalf("fresh tab: reset=%v catchup=%q, want reset and the history", first.reset, first.catchup)
	}
	s.output([]byte("seen"))
	first.readUntil(func() bool { return len(first.live) == 4 })
	_ = first.conn.Close()

	s.output([]byte("missed"))

	second := dial(t, srv, fmt.Sprintf("?epoch=%s&from=%d", first.epoch, first.pos))
	second.readUntil(second.isLive)
	if second.reset || string(second.catchup) != "missed" {
		t.Fatalf("resume: reset=%v catchup=%q, want no reset and %q", second.reset, second.catchup, "missed")
	}
	s.output([]byte("after"))
	second.readUntil(func() bool { return len(second.live) == 5 })
	if string(second.live) != "after" {
		t.Fatalf("live after resume = %q, want %q", second.live, "after")
	}
}
