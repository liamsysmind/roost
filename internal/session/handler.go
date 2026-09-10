package session

import (
	"log"
	"net/http"
	"strconv"
	"strings"
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

	client := NewClient(conn)
	defer client.Close()

	if err := s.Attach(client); err != nil {
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
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	// Reader loop blocks here until the client disconnects.
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		switch mt {
		case websocket.BinaryMessage:
			if err := s.Input(data); err != nil {
				return
			}
		case websocket.TextMessage:
			handleControl(s, string(data))
		}
	}
}

// handleControl interprets text-frame commands.
//
// Today only "resize ROWS COLS" is supported.
func handleControl(s *Session, msg string) {
	parts := strings.Fields(msg)
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
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
