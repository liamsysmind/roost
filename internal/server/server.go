// Package server wires HTTP routes and static assets together.
package server

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/liamsysmind/roost/internal/auth"
	"github.com/liamsysmind/roost/internal/session"
)

//go:embed all:web
var webFS embed.FS

type Server struct {
	Auth     *auth.Manager
	Sessions *session.Manager
	Addr     string
	handler  http.Handler
	static   fs.FS
}

func New(a *auth.Manager, sm *session.Manager, addr string) *Server {
	static, _ := fs.Sub(webFS, "web")
	s := &Server{Auth: a, Sessions: sm, Addr: addr, static: static}
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

	wsh := &session.Handler{Manager: s.Sessions}
	mux.HandleFunc("GET /ws/terminal", wsh.Serve)
	mux.HandleFunc("GET /ws/terminal/{id}", wsh.Serve)

	mux.HandleFunc("GET /", s.handleIndex)

	s.handler = s.authMiddleware(mux)
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r.URL.Path) {
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
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
	}
	_, _ = w.Write(b)
}

func (s *Server) Run() error {
	log.Printf("roost listening on http://%s", s.Addr)
	return http.ListenAndServe(s.Addr, s.handler)
}
