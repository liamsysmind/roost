package session

import (
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Client wraps a single WebSocket connection attached to a Session.
//
// Output does not pass through a queue. The client holds a position in the
// session's log and WriteLoop reads forward from it, so nothing is ever
// dropped: a tab that cannot keep up falls behind, and the bytes wait on
// disk. The session's read loop only nudges `wake` after each append.
//
// The previous design fanned chunks into a 64-deep channel per client and
// discarded whatever did not fit. Under heavy output that silently removed
// line feeds and cursor moves from the stream, so lines never reached the
// browser's scrollback and the screen stayed corrupt until the next redraw.
type Client struct {
	conn *websocket.Conn
	log  *Log
	wake chan struct{} // capacity 1; a pending nudge is enough

	// Set by start before WriteLoop runs. WriteLoop releases snap.Data once
	// sent; everything else here is read-only after start.
	snap         Snapshot
	catchupTotal int
	pos          atomic.Int64 // next logical offset of live output to send

	closeOnce sync.Once
	closed    chan struct{}

	// writeErr is why WriteLoop stopped, if a write failed. The handler reports
	// it when the connection closes, so a disconnect caused by the outbound
	// side is not mistaken for one the reader saw.
	writeErr atomic.Pointer[error]

	// Catch-up progress, for the close line.
	replaySent   atomic.Int64
	replayDoneAt atomic.Int64 // UnixNano; 0 until the catch-up has been sent
}

// NewClient wraps an upgraded WebSocket. Caller must invoke Close exactly once.
func NewClient(conn *websocket.Conn) *Client {
	return &Client{
		conn:   conn,
		wake:   make(chan struct{}, 1),
		closed: make(chan struct{}),
	}
}

// start records what Attach computed. Must be called before WriteLoop.
func (c *Client) start(l *Log, snap Snapshot) {
	c.log = l
	c.snap = snap
	c.catchupTotal = len(snap.Data)
	c.pos.Store(snap.LiveFrom)
}

// notify tells WriteLoop the log has grown. Never blocks the session.
func (c *Client) notify() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Lag is how many bytes of live output this client has not been sent yet.
func (c *Client) Lag() int64 {
	if c.log == nil {
		return 0
	}
	return c.log.End() - c.pos.Load()
}

// frameSize bounds each binary frame. xterm.js parses incoming data on the
// main thread; a single multi-MB frame freezes the tab until parsing
// finishes, with no opportunity to repaint.
const frameSize = 64 * 1024

// pingInterval is how often we send a WS ping frame to keep idle proxies
// (notably Cloudflare's ~100s WebSocket idle timeout) from dropping the
// connection while the user just stares at the terminal. Browsers respond
// to pings transparently per the WS spec, so no JS-side cooperation needed.
// Variables rather than constants so a test can shrink them; nothing in
// production reassigns them.
var pingInterval = 30 * time.Second

// pongWait is how long a connection may go without a single frame from the
// client before it is treated as dead. Without it, a connection that died at
// the transport layer — a phone that slept, a Wi-Fi handover, a tunnel that
// went away — leaves ReadMessage blocked indefinitely until the OS TCP
// keepalive gives up, which on macOS is about two hours.
//
// A tab that is alive but not reading — Chrome throttles hidden tabs — also
// runs into this. That costs nothing now: the bytes stay in the log and the
// tab resumes from its position when it reconnects.
//
// Comfortably over two ping intervals, so one lost ping or pong does not tear
// down a healthy connection.
var pongWait = 70 * time.Second

// control is a text frame. The browser tells these apart from the plain-text
// error messages by the "roost" key.
type control struct {
	Kind  string `json:"roost"`
	Epoch string `json:"epoch,omitempty"`
	Pos   int64  `json:"pos,omitempty"`
	Reset bool   `json:"reset,omitempty"`
}

func (c *Client) writeControl(m control) error {
	b, _ := json.Marshal(m)
	return c.write(websocket.TextMessage, b)
}

func (c *Client) write(mt int, b []byte) error {
	if err := c.conn.WriteMessage(mt, b); err != nil {
		c.writeErr.Store(&err)
		return err
	}
	return nil
}

// WriteLoop sends the catch-up computed by Attach, then follows the log until
// Close is called or the connection errors out.
//
// Protocol, in order:
//
//	{"roost":"start","epoch":E,"reset":R}  clear the terminal first if R
//	binary frames                          catch-up, queries stripped
//	{"roost":"live","pos":P}               the client now holds everything before P
//	binary frames                          live output; each byte advances P
//	{"roost":"hb"}                         every pingInterval, so the browser
//	                                       can tell a silent shell from a dead socket
//
// Pings go out between frames, catch-up included. Previously they started only
// after the replay drained, so a tab slow to read 4 MB of replay got no ping,
// sent no pong, and was disconnected as dead after pongWait.
func (c *Client) WriteLoop() {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	heartbeat := func() error {
		if err := c.write(websocket.PingMessage, nil); err != nil {
			return err
		}
		return c.writeControl(control{Kind: "hb"})
	}
	// tick sends a heartbeat if one is due, without waiting.
	tick := func() error {
		select {
		case <-t.C:
			return heartbeat()
		default:
			return nil
		}
	}

	if c.writeControl(control{Kind: "start", Epoch: c.log.Epoch(), Reset: c.snap.Reset}) != nil {
		return
	}
	for data := c.snap.Data; len(data) > 0; {
		select {
		case <-c.closed:
			return
		default:
		}
		n := min(frameSize, len(data))
		if c.write(websocket.BinaryMessage, data[:n]) != nil || tick() != nil {
			return
		}
		data = data[n:]
		c.replaySent.Add(int64(n))
	}
	c.snap.Data = nil
	if c.writeControl(control{Kind: "live", Pos: c.pos.Load()}) != nil {
		return
	}
	c.replayDoneAt.Store(time.Now().UnixNano())

	buf := make([]byte, frameSize)
	for {
		n, err := c.log.ReadAt(buf, c.pos.Load())
		switch {
		case n > 0:
			if c.write(websocket.BinaryMessage, buf[:n]) != nil || tick() != nil {
				return
			}
			c.pos.Add(int64(n))
			continue
		case errors.Is(err, ErrTooOld):
			// Rotated past this client while it was not reading. Closing makes
			// it reconnect from its position, which Attach answers with a reset
			// and the tail — the same as a fresh tab.
			c.closeWith(4001, "fell behind")
			return
		case err != nil && !errors.Is(err, io.EOF):
			c.writeErr.Store(&err)
			return
		}
		select {
		case <-c.wake:
		case <-t.C:
			if heartbeat() != nil {
				return
			}
		case <-c.closed:
			return
		}
	}
}

// closeWith sends a WebSocket close frame and signals the writer to stop.
// Safe to call from any goroutine: WriteControl may run concurrently with
// WriteMessage, which WriteMessage(CloseMessage) may not.
func (c *Client) closeWith(code int, reason string) {
	c.closeOnce.Do(func() {
		_ = c.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
		close(c.closed)
	})
}

// Close releases the client. Safe to call multiple times.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.closed)
	})
}
