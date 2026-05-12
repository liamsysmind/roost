// Package notify implements an in-process pub/sub for push notifications.
//
// Subscribers are typically SSE-backed HTTP handlers. Publishers are either
// HTTP POSTs from Claude Code Stop hooks (carrying X-Roost-Hook-Secret) or
// the cookie-authenticated UI itself.
package notify

import (
	"sync"
	"time"
)

// Notification is the canonical shape pushed to clients.
type Notification struct {
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Session   string    `json:"session,omitempty"`
	Source    string    `json:"source,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Notifier broadcasts notifications to every subscriber. Slow subscribers
// get their queued events dropped rather than holding everyone else up.
type Notifier struct {
	mu   sync.Mutex
	subs map[chan Notification]struct{}
}

func NewNotifier() *Notifier {
	return &Notifier{subs: map[chan Notification]struct{}{}}
}

// Subscribe returns a buffered channel that receives every future
// notification. Call Unsubscribe when done.
func (n *Notifier) Subscribe() chan Notification {
	ch := make(chan Notification, 16)
	n.mu.Lock()
	n.subs[ch] = struct{}{}
	n.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (n *Notifier) Unsubscribe(ch chan Notification) {
	n.mu.Lock()
	if _, ok := n.subs[ch]; ok {
		delete(n.subs, ch)
		close(ch)
	}
	n.mu.Unlock()
}

// Publish fans the notification out to every subscriber. Non-blocking;
// drops to slow consumers.
func (n *Notifier) Publish(notif Notification) {
	if notif.Timestamp.IsZero() {
		notif.Timestamp = time.Now()
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for ch := range n.subs {
		select {
		case ch <- notif:
		default:
		}
	}
}
