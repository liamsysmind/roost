package session

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		return origin == "http://"+r.Host || origin == "https://"+r.Host
	},
}

// Handler is the HTTP entry point for terminal WebSocket connections.
type Handler struct {
	Manager *Manager
}

// Serve handles GET /ws/terminal[/{id}]. The "id" path value is optional;
// when absent we use a single "default" session.
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		id = "default"
	}

	s, err := h.Manager.GetOrCreate(id)
	if err != nil {
		log.Printf("session create %s: %v", id, err)
		http.Error(w, "session unavailable", http.StatusInternalServerError)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	// Every connection gets an open line and a close line with the cause.
	// Disconnects used to leave a trace only when output had been dropped, and
	// cloudflared does not log WebSocket closes at all, so there was nothing
	// to tell a slept phone from a tunnel drop from roost closing the socket.
	peer := peerOf(r)
	start := time.Now()
	var lastRx atomic.Int64
	lastRx.Store(start.UnixNano())
	log.Printf("ws open session=%s peer=%s ua=%s", id, peer, uaClass(r.UserAgent()))

	client := NewClient(conn)
	cause := "unknown"
	defer func() {
		client.Close()
		if p := client.writeErr.Load(); p != nil {
			cause += fmt.Sprintf("; write failed first: %v", *p)
		}
		catchup := fmt.Sprintf("%dKB/%dKB unfinished",
			client.replaySent.Load()/1024, client.catchupTotal/1024)
		if done := client.replayDoneAt.Load(); done != 0 {
			catchup = fmt.Sprintf("%dKB in %s", client.replaySent.Load()/1024,
				time.Unix(0, done).Sub(start).Round(100*time.Millisecond))
		}
		if client.snap.Reset {
			catchup += " reset"
		}
		log.Printf("ws close session=%s peer=%s dur=%s last_rx=%s lag=%d catchup=%q cause=%q",
			id, peer, time.Since(start).Round(time.Second),
			time.Since(time.Unix(0, lastRx.Load())).Round(100*time.Millisecond),
			client.Lag(), catchup, cause)
	}()

	// A reconnecting tab names the log position it has reached, so it is sent
	// only what it missed. Anything unusable (absent, malformed, another
	// epoch) gets a reset and the tail, which is what a fresh tab gets.
	from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
	if err := s.Attach(client, r.URL.Query().Get("epoch"), from); err != nil {
		cause = "attach: " + err.Error()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(err.Error()))
		return
	}
	defer s.Detach(client)

	go client.WriteLoop()

	// Liveness. WriteLoop already sends a ping every pingInterval; browsers
	// answer it transparently. This is the other half: if nothing at all comes
	// back within pongWait the read fails, Serve returns, and the deferred
	// Close tears the client out of the broadcast set. Any frame counts as
	// proof of life — a pong, a keystroke, a resize.
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		lastRx.Store(time.Now().UnixNano())
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	// Reader loop blocks here until the client disconnects.
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			cause = readCause(err)
			return
		}
		lastRx.Store(time.Now().UnixNano())
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		switch mt {
		case websocket.BinaryMessage:
			if err := s.Input(data); err != nil {
				cause = "pty input: " + err.Error()
				return
			}
		case websocket.TextMessage:
			handleControl(s, peer, string(data))
		}
	}
}

// readCause names why ReadMessage failed in terms of who ended the connection.
func readCause(err error) string {
	var ce *websocket.CloseError
	if errors.As(err, &ce) {
		return fmt.Sprintf("close frame code=%d reason=%q", ce.Code, ce.Text)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Sprintf("no frame from client for %s", pongWait)
	}
	return "read: " + err.Error()
}

// peerOf is the client address as the operator would recognise it. Behind
// Cloudflare RemoteAddr is always cloudflared on loopback.
func peerOf(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// uaClass reduces a User-Agent to the distinction that matters for
// disconnects: phones sleep and suspend sockets, desktops mostly do not.
func uaClass(ua string) string {
	switch {
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		return "ios"
	case strings.Contains(ua, "Android"):
		return "android"
	case strings.Contains(ua, "Macintosh"):
		return "mac"
	case ua == "":
		return "none"
	}
	return "other"
}

// handleControl interprets text-frame commands: "resize ROWS COLS", and
// "note TEXT", which the browser sends after reconnecting to report how the
// previous socket ended from its side — the close code, whether the tab was
// hidden or frozen. The server cannot see any of that.
func handleControl(s *Session, peer, msg string) {
	parts := strings.Fields(msg)
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "note":
		note := strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, strings.TrimSpace(strings.TrimPrefix(msg, "note")))
		if len(note) > 300 {
			note = note[:300]
		}
		log.Printf("ws note session=%s peer=%s %s", s.ID, peer, note)
	case "resize":
		if len(parts) != 3 {
			return
		}
		rows, e1 := strconv.Atoi(parts[1])
		cols, e2 := strconv.Atoi(parts[2])
		if e1 != nil || e2 != nil || rows <= 0 || cols <= 0 {
			return
		}
		if err := s.Resize(uint16(rows), uint16(cols)); err != nil {
			log.Printf("session %s resize: %v", s.ID, err)
		}
	}
}
