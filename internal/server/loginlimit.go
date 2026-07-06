package server

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// loginLimiter throttles password guesses per client IP. roost's password is
// the only wall in front of a shell, and handleLoginPost otherwise has no
// brake — an attacker could grind bcrypt guesses as fast as they can open
// connections. After a small free window, each further failure from the same
// IP earns an exponentially growing lockout (capped), which turns an online
// brute-force from "thousands per minute" into "a handful per hour".
//
// Keyed per IP (not globally) so a flood from one source can't lock the real
// user out from another. Behind Cloudflare the key comes from CF-Connecting-IP;
// the origin is only reachable through the tunnel, so that header is set by
// Cloudflare and can't be forged by a client hitting us directly.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*attemptState
}

type attemptState struct {
	fails        int
	blockedUntil time.Time
	seen         time.Time
}

const (
	loginFreeAttempts = 5                // failures allowed before backoff starts
	loginBackoffBase  = 2 * time.Second  // first penalty; doubles each further fail
	loginBackoffMax   = 15 * time.Minute // ceiling on a single lockout
	loginEntryTTL     = 30 * time.Minute // idle entries are reaped
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: map[string]*attemptState{}}
}

// retryAfter reports how long key must wait before another attempt is allowed;
// 0 means "go ahead".
func (l *loginLimiter) retryAfter(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.attempts[key]
	if st == nil {
		return 0
	}
	if now.Before(st.blockedUntil) {
		return st.blockedUntil.Sub(now)
	}
	return 0
}

func (l *loginLimiter) recordFailure(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reap(now)
	st := l.attempts[key]
	if st == nil {
		st = &attemptState{}
		l.attempts[key] = st
	}
	st.fails++
	st.seen = now
	if st.fails > loginFreeAttempts {
		shift := uint(st.fails - loginFreeAttempts - 1)
		d := loginBackoffBase << shift
		if d > loginBackoffMax || d <= 0 { // d<=0 guards the shift overflowing int64
			d = loginBackoffMax
		}
		st.blockedUntil = now.Add(d)
	}
}

func (l *loginLimiter) recordSuccess(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

// reap drops entries idle longer than the TTL. Caller holds the lock.
func (l *loginLimiter) reap(now time.Time) {
	for k, st := range l.attempts {
		if now.Sub(st.seen) > loginEntryTTL {
			delete(l.attempts, k)
		}
	}
}

// clientIP prefers Cloudflare's connecting-IP header (see loginLimiter doc),
// falling back to the transport peer for direct/loopback access.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
