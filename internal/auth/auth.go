// Package auth handles password verification and cookie-backed sessions.
//
// Sessions are stored in-memory; restarting the server logs everyone out.
// That's acceptable for a single-user self-hosted tool.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	CookieName = "roost_session"
	SessionTTL = 24 * time.Hour
)

type Manager struct {
	passwordHash []byte
	mu           sync.Mutex
	sessions     map[string]time.Time
}

// Session IDs are 32 bytes of crypto/rand stored in an in-memory map keyed
// off the cookie value. There's no signing key — restarting the server
// drops the map and logs everyone out, which is fine for a single-user tool.
func NewManager(passwordHash string) *Manager {
	return &Manager{
		passwordHash: []byte(passwordHash),
		sessions:     map[string]time.Time{},
	}
}

func (m *Manager) Verify(password string) bool {
	return bcrypt.CompareHashAndPassword(m.passwordHash, []byte(password)) == nil
}

func (m *Manager) CreateSession() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	m.mu.Lock()
	m.sessions[id] = time.Now().Add(SessionTTL)
	m.mu.Unlock()
	return id
}

func (m *Manager) ValidateSession(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.sessions[id]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(m.sessions, id)
		return false
	}
	return true
}

func (m *Manager) DropSession(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

// secure marks the cookie Secure so it only rides HTTPS. It's a parameter
// rather than a constant because roost is reached two ways: over HTTPS through
// Cloudflare (where we want Secure) and over plain HTTP on loopback via an SSH
// tunnel (where a Secure cookie would never be sent, breaking local login).
// The caller decides based on the request's forwarded proto.
func (m *Manager) SetCookie(w http.ResponseWriter, sessionID string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(SessionTTL.Seconds()),
	})
}

func (m *Manager) ClearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
