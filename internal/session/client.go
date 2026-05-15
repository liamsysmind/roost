package session

import (
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Client wraps a single WebSocket connection attached to a Session.
// Output is queued through a buffered channel; a writer goroutine
// drains the channel and writes to the connection.
type Client struct {
	conn *websocket.Conn
	out  chan []byte

	// replay is scrollback handed to us by Attach; WriteLoop streams it
	// out as small frames before draining live broadcast. Owned solely by
	// WriteLoop after Attach returns — `go WriteLoop()` happens-after the
	// queueReplay write, so no mutex is needed.
	replay []byte

	closeOnce sync.Once
	closed    chan struct{}

	dropped int // bytes dropped due to slow consumer (best-effort, racy is fine)
}

const clientQueueDepth = 64

// NewClient wraps an upgraded WebSocket. Caller must invoke Close exactly once.
func NewClient(conn *websocket.Conn) *Client {
	return &Client{
		conn:   conn,
		out:    make(chan []byte, clientQueueDepth),
		closed: make(chan struct{}),
	}
}

// send queues data for delivery. If the queue is full, drops the chunk
// rather than blocking the producer (the broadcaster). A slow tab will
// fall behind and see drops in their output — accepted trade-off vs
// blocking everyone else.
func (c *Client) send(b []byte) {
	select {
	case c.out <- b:
	case <-c.closed:
	default:
		// Slow consumer.
		c.dropped += len(b)
	}
}

// queueReplay records scrollback to be streamed out by WriteLoop before any
// live broadcast. Caller (Session.Attach) holds the session mutex while
// calling this and while adding the client to the broadcast set, which is
// what keeps replay-then-live ordering intact. Must be called at most once,
// before WriteLoop is started.
func (c *Client) queueReplay(b []byte) {
	c.replay = b
}

// replayChunkSize bounds each scrollback frame. xterm.js parses incoming
// data on the main thread; a single multi-MB frame freezes the tab until
// parsing finishes, with no opportunity to repaint. Splitting into ~64 KB
// frames lets the browser interleave parsing with rendering so the user
// sees the terminal fill in progressively instead of staring at a blank
// page.
const replayChunkSize = 64 * 1024

// pingInterval is how often we send a WS ping frame to keep idle proxies
// (notably Cloudflare's ~100s WebSocket idle timeout) from dropping the
// connection while the user just stares at the terminal. Browsers respond
// to pings transparently per the WS spec, so no JS-side cooperation needed.
const pingInterval = 30 * time.Second

// WriteLoop pumps the out channel to the WebSocket until Close is called
// or the connection errors out. Also drives a ping ticker so connections
// behind idle-timing proxies stay alive. If Attach handed us scrollback
// via queueReplay, that is streamed out in chunks first.
func (c *Client) WriteLoop() {
	if !c.drainReplay() {
		return
	}
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case b, ok := <-c.out:
			if !ok {
				return
			}
			if err := c.conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
				return
			}
		case <-t.C:
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.closed:
			return
		}
	}
}

// drainReplay streams the queued scrollback as chunked binary frames.
// Returns false if the connection died or the client was closed mid-way,
// in which case the caller (WriteLoop) should bail. Live bytes that arrive
// during replay queue up in c.out and are delivered immediately after.
func (c *Client) drainReplay() bool {
	for len(c.replay) > 0 {
		select {
		case <-c.closed:
			return false
		default:
		}
		n := replayChunkSize
		if n > len(c.replay) {
			n = len(c.replay)
		}
		if err := c.conn.WriteMessage(websocket.BinaryMessage, c.replay[:n]); err != nil {
			return false
		}
		c.replay = c.replay[n:]
	}
	c.replay = nil
	return true
}

// closeWith sends a WebSocket close frame and signals the writer to stop.
func (c *Client) closeWith(code int, reason string) {
	c.closeOnce.Do(func() {
		_ = c.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason))
		close(c.closed)
		if c.dropped > 0 {
			log.Printf("client: dropped %d bytes due to slow consumer", c.dropped)
		}
	})
}

// Close releases the client. Safe to call multiple times.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		if c.dropped > 0 {
			log.Printf("client: dropped %d bytes due to slow consumer", c.dropped)
		}
	})
}
