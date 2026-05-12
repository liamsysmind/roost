// Package terminal proxies a WebSocket to a PTY running the user's shell.
//
// Wire protocol:
//   - Binary messages   = raw TTY data (both directions)
//   - Text messages from client = control commands; only "resize ROWS COLS" today.
package terminal

import (
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// SSH-tunnel deployments expose roost on 127.0.0.1, so requests must
	// come from the same Host header. Empty Origin (curl, native clients)
	// is also allowed.
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		return origin == "http://"+r.Host || origin == "https://"+r.Host
	},
}

func HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "ROOST=1")

	f, err := pty.Start(cmd)
	if err != nil {
		log.Printf("pty start: %v", err)
		_ = conn.WriteMessage(websocket.TextMessage, []byte("pty start: "+err.Error()))
		return
	}
	defer func() {
		_ = f.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()

	var writeMu sync.Mutex
	send := func(t int, b []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(t, b)
	}

	// PTY → WebSocket
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				if werr := send(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("pty read: %v", err)
				}
				_ = send(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shell exited"))
				return
			}
		}
	}()

	// WebSocket → PTY
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch mt {
		case websocket.BinaryMessage, websocket.TextMessage:
			if mt == websocket.TextMessage && len(data) > 0 && data[0] >= 'a' && data[0] <= 'z' {
				if handleControl(f, string(data)) {
					continue
				}
			}
			if _, err := f.Write(data); err != nil {
				return
			}
		}
	}
}

// handleControl interprets text-frame control commands. Returns true if
// the message was consumed as a control command.
func handleControl(f *os.File, msg string) bool {
	parts := strings.Fields(msg)
	if len(parts) == 0 {
		return false
	}
	switch parts[0] {
	case "resize":
		if len(parts) != 3 {
			return true
		}
		rows, err1 := strconv.Atoi(parts[1])
		cols, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || rows <= 0 || cols <= 0 {
			return true
		}
		if err := pty.Setsize(f, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}); err != nil {
			log.Printf("pty setsize: %v", err)
		}
		return true
	}
	return false
}
