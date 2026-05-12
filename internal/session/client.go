package session

import (
	"log"
	"sync"

	"github.com/gorilla/websocket"
)

// Client wraps a single WebSocket connection attached to a Session.
// Output is queued through a buffered channel; a writer goroutine
// drains the channel and writes to the connection.
type Client struct {
	conn *websocket.Conn
	out  chan []byte

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

// WriteLoop pumps the out channel to the WebSocket until Close is called
// or the connection errors out.
func (c *Client) WriteLoop() {
	for {
		select {
		case b, ok := <-c.out:
			if !ok {
				return
			}
			if err := c.conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
				return
			}
		case <-c.closed:
			return
		}
	}
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
