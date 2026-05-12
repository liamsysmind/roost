// Package session manages persistent terminal sessions whose lifetimes
// are decoupled from any single WebSocket connection.
//
// Each session owns:
//   - a PTY running a shell
//   - an append-only log file on disk (the source of truth for scrollback)
//   - a set of currently-attached clients that get broadcast new output
//
// The log file is effectively unbounded — scrollback grows with disk space.
// `ReplayBytes` only controls how much of the tail is replayed when a new
// client attaches, so reconnects stay responsive.
package session

import (
	"errors"
	"io"
	"os"
	"sync/atomic"
)

// Log is an append-only on-disk buffer of session output.
// Writes are sequential; reads (for replay) can happen concurrently
// at any offset via positional ReadAt.
type Log struct {
	f    *os.File
	size atomic.Int64
}

func openLog(path string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	l := &Log{f: f}
	l.size.Store(info.Size())
	return l, nil
}

func (l *Log) Write(p []byte) (int, error) {
	if l == nil || l.f == nil {
		return len(p), nil // log disabled — silently accept
	}
	n, err := l.f.Write(p)
	if n > 0 {
		l.size.Add(int64(n))
	}
	return n, err
}

// Snapshot returns the last `tailBytes` of the log. If tailBytes <= 0
// or larger than the log, returns the entire log.
func (l *Log) Snapshot(tailBytes int64) ([]byte, error) {
	if l == nil || l.f == nil {
		return nil, nil
	}
	sz := l.size.Load()
	if sz == 0 {
		return nil, nil
	}
	start := int64(0)
	if tailBytes > 0 && sz > tailBytes {
		start = sz - tailBytes
	}
	out := make([]byte, sz-start)
	_, err := l.f.ReadAt(out, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return out, nil
}

// Size returns the current log size in bytes.
func (l *Log) Size() int64 {
	if l == nil {
		return 0
	}
	return l.size.Load()
}

// Close closes the underlying file. After Close, further writes are no-ops.
func (l *Log) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Remove deletes the underlying log file. Should be called only after Close.
func (l *Log) Remove(path string) error {
	if path == "" {
		return nil
	}
	return os.Remove(path)
}
