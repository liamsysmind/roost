// Package server wires HTTP routes and static assets together.
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/liamsysmind/roost/internal/ai"
	"github.com/liamsysmind/roost/internal/auth"
	rfs "github.com/liamsysmind/roost/internal/fs"
	"github.com/liamsysmind/roost/internal/notify"
	"github.com/liamsysmind/roost/internal/session"
)

//go:embed all:web
var webFS embed.FS

type Server struct {
	Auth           *auth.Manager
	Sessions       *session.Manager
	FS             *rfs.API
	Notifier       *notify.Notifier
	HookSecret     string
	Addr           string
	Version        string
	IdleAlertAfter time.Duration
	handler        http.Handler
	static         fs.FS
	aiReader       *ai.Reader
	aiCodex        *ai.CodexReader
}

func New(a *auth.Manager, sm *session.Manager, fsAPI *rfs.API, n *notify.Notifier, hookSecret, addr, version string, idleAlertAfter time.Duration) *Server {
	static, _ := fs.Sub(webFS, "web")
	s := &Server{
		Auth: a, Sessions: sm, FS: fsAPI, Notifier: n,
		HookSecret: hookSecret, Addr: addr, Version: version,
		IdleAlertAfter: idleAlertAfter,
		static:         static,
		aiReader:       ai.NewReader(),
		aiCodex:        ai.NewCodexReader(),
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", s.handleLoginGet)
	mux.HandleFunc("POST /login", s.handleLoginPost)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /vendor/", s.handleStatic)
	mux.HandleFunc("GET /app.js", s.handleStatic)
	mux.HandleFunc("GET /home.js", s.handleStatic)
	mux.HandleFunc("GET /fs.js", s.handleStatic)
	mux.HandleFunc("GET /notify.js", s.handleStatic)
	mux.HandleFunc("GET /notify-worker.js", s.handleStatic)
	mux.HandleFunc("GET /toast.js", s.handleStatic)
	mux.HandleFunc("GET /ai.js", s.handleStatic)
	mux.HandleFunc("GET /sessions.js", s.handleStatic)

	wsh := &session.Handler{Manager: s.Sessions}
	mux.HandleFunc("GET /ws/terminal", wsh.Serve)
	mux.HandleFunc("GET /ws/terminal/{id}", wsh.Serve)

	mux.HandleFunc("GET /api/sessions", s.handleSessionsList)
	mux.HandleFunc("GET /api/sessions/status", s.handleSessionsStatus)
	mux.HandleFunc("GET /api/sessions/{id}/cwd", s.handleSessionCwd)
	mux.HandleFunc("POST /api/sessions/{id}/rename", s.handleSessionRename)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleSessionDelete)


	(&rfs.Handler{API: s.FS}).Mount(mux)
	(&ai.Handler{
		Reader:      s.aiReader,
		CodexReader: s.aiCodex,
		CwdForSession: func(sid string) string {
			c, _ := s.Sessions.Cwd(sid)
			return c
		},
	}).Mount(mux)

	mux.HandleFunc("GET /api/notify/stream", s.handleNotifyStream)
	mux.HandleFunc("POST /api/notify", s.handleNotifyPost)

	mux.HandleFunc("GET /", s.handleHome)
	mux.HandleFunc("GET /s/{id}", s.handleIndex)

	s.handler = s.authMiddleware(mux)
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// POST /api/notify accepts X-Roost-Hook-Secret instead of a cookie,
		// so local hooks can push notifications without holding a session.
		if r.Method == http.MethodPost && r.URL.Path == "/api/notify" &&
			s.HookSecret != "" && r.Header.Get("X-Roost-Hook-Secret") == s.HookSecret {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(auth.CookieName)
		if err != nil || !s.Auth.ValidateSession(c.Value) {
			if strings.HasPrefix(r.URL.Path, "/ws/") || strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isPublicPath(p string) bool {
	if p == "/login" {
		return true
	}
	if strings.HasPrefix(p, "/vendor/") {
		return true
	}
	return false
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.static, "login.html")
	if err != nil {
		http.Error(w, "login template missing", http.StatusInternalServerError)
		return
	}
	errMsg := r.URL.Query().Get("error")
	body := strings.ReplaceAll(string(b), "{{ERROR}}", errMsg)
	body = strings.ReplaceAll(body, "{{VERSION}}", s.Version)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.Auth.Verify(r.FormValue("password")) {
		http.Redirect(w, r, "/login?error=incorrect+password", http.StatusSeeOther)
		return
	}
	s.Auth.SetCookie(w, s.Auth.CreateSession())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		s.Auth.DropSession(c.Value)
	}
	s.Auth.ClearCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.static, "index.html")
	if err != nil {
		http.Error(w, "index template missing", http.StatusInternalServerError)
		return
	}
	body := strings.ReplaceAll(string(b), "{{VERSION}}", s.Version)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The page carries inline CSS that changes between deploys, so it falls
	// under the same revalidation rule as our JS/CSS in handleStatic —
	// otherwise browsers serve a stale layout even after reload.
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	_, _ = w.Write([]byte(body))
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.static, "home.html")
	if err != nil {
		http.Error(w, "home template missing", http.StatusInternalServerError)
		return
	}
	body := strings.ReplaceAll(string(b), "{{VERSION}}", s.Version)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	_, _ = w.Write([]byte(body))
}

func (s *Server) handleSessionsList(w http.ResponseWriter, r *http.Request) {
	list := s.Sessions.List()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (s *Server) handleSessionRename(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	to := strings.TrimSpace(r.FormValue("to"))
	if to == "" {
		http.Error(w, "missing 'to' parameter", http.StatusBadRequest)
		return
	}
	if err := s.Sessions.Rename(id, to); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Sessions.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSessionCwd(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cwd, cmd, app, err := s.Sessions.PaneInfo(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"cwd":     cwd,
		"command": cmd,
		"app":     app,
	})
}

// SessionStatus is one row in the Sessions tab. It flattens the session.Info
// snapshot together with the live tmux pane info and (for agent sessions) the
// AI reader's view of the active conversation.
type SessionStatus struct {
	session.Info
	Cwd           string `json:"cwd,omitempty"`
	Cmd           string `json:"cmd,omitempty"`
	App           string `json:"app,omitempty"`
	Model         string `json:"model,omitempty"`
	ContextTokens int64  `json:"context_tokens,omitempty"`
	InputTokens   int64  `json:"input_tokens,omitempty"`
	OutputTokens  int64  `json:"output_tokens,omitempty"`
}

// handleSessionsStatus returns one SessionStatus per session known to the
// manager. PaneInfo and the AI reader are queried in parallel because each
// PaneInfo call forks a tmux process and the AI lookup walks a JSONL file;
// serial would push the 5s poll budget past comfort once there are a handful
// of sessions.
func (s *Server) handleSessionsStatus(w http.ResponseWriter, r *http.Request) {
	list := s.Sessions.List()
	out := make([]SessionStatus, len(list))
	var wg sync.WaitGroup
	for i, info := range list {
		out[i].Info = info
		if info.Closed {
			continue
		}
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			cwd, cmd, app, err := s.Sessions.PaneInfo(id)
			if err != nil {
				return
			}
			out[i].Cwd = cwd
			out[i].Cmd = cmd
			out[i].App = app
			if cwd == "" {
				return
			}
			var active *ai.ActiveSession
			switch app {
			case "claude":
				if s.aiReader != nil {
					active, _ = s.aiReader.Active(cwd)
				}
			case "codex":
				if s.aiCodex != nil {
					active, _ = s.aiCodex.Active(cwd)
				}
			}
			if active != nil {
				out[i].Model = active.Model
				out[i].ContextTokens = active.ContextTokens
				out[i].InputTokens = active.Usage.InputTokens
				out[i].OutputTokens = active.Usage.OutputTokens
			}
		}(i, info.ID)
	}
	wg.Wait()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	b, err := fs.ReadFile(s.static, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(path, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(path, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	if strings.HasPrefix(path, "vendor/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// roost's own JS/CSS changes between deploys. Without an explicit
		// directive browsers apply heuristic freshness and silently serve
		// stale bundles — users see the previous build's behavior even after
		// a reload. Force revalidation so every deploy takes effect.
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	}
	_, _ = w.Write(b)
}

func (s *Server) handleNotifyStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := s.Notifier.Subscribe()
	defer s.Notifier.Unsubscribe(ch)

	// Send a hello so the client knows the stream is live, and pass the
	// idle-alert threshold so the client can gate Stop-hook notifications
	// without re-fetching config on every event.
	helloData, _ := json.Marshal(map[string]any{
		"idle_alert_after_ms": s.IdleAlertAfter.Milliseconds(),
	})
	fmt.Fprintf(w, "event: hello\ndata: %s\n\n", helloData)
	flusher.Flush()

	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case n, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(n)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-tick.C:
			// Comment lines act as SSE keep-alives.
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) handleNotifyPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	body := strings.TrimSpace(r.FormValue("body"))
	if title == "" && body == "" {
		http.Error(w, "title or body is required", http.StatusBadRequest)
		return
	}
	if title == "" {
		title = "roost"
	}
	source := "hook"
	if c, err := r.Cookie(auth.CookieName); err == nil && s.Auth.ValidateSession(c.Value) {
		source = "ui"
	}
	s.Notifier.Publish(notify.Notification{
		Title:   title,
		Body:    body,
		Session: r.FormValue("session"),
		Cwd:     strings.TrimSpace(r.FormValue("cwd")),
		Source:  source,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) Run() error {
	log.Printf("roost listening on http://%s", s.Addr)
	return http.ListenAndServe(s.Addr, s.handler)
}
