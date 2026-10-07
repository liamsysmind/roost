// Package session manages persistent terminal sessions whose lifetimes
// are decoupled from any single WebSocket connection.
//
// Each session owns:
//   - a PTY running a shell
//   - a bounded on-disk log of its output (the source of truth for scrollback)
//   - a set of currently-attached clients, each streaming from the log
//
// Clients do not receive output through an in-memory queue. Each one keeps a
// position in the log and reads forward at its own pace, so a slow tab falls
// behind instead of losing bytes, and a reconnecting tab resumes from where
// it stopped. The log is kept in two segments and the older one is deleted
// when the newer fills, so disk use per session is capped.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// ErrTooOld means the requested offset has been rotated out of the log.
var ErrTooOld = errors.New("offset rotated out of the log")

var errLogClosed = errors.New("log closed")

// Log is a two-segment on-disk buffer of session output, addressed by a
// logical offset that only grows. `path` holds the newer segment and
// `path.prev` the older; when the newer reaches segMax it becomes the older
// and the previous older segment is deleted.
//
// Offsets mean nothing outside one Log value: they restart at 0 every time a
// log is opened. Epoch names the value, so a client holding an offset from a
// previous roost process is recognised and given a fresh replay instead of a
// resume at a position that now points somewhere else.
type Log struct {
	mu        sync.RWMutex // guards the segment fields below against rotation
	path      string
	cur       *os.File
	prev      *os.File // nil when there is no older segment
	prevStart int64    // logical offset of prev's first byte
	curStart  int64    // logical offset of cur's first byte

	end    atomic.Int64 // logical offset one past the last byte written
	segMax int64
	epoch  string
}

func prevPath(path string) string { return path + ".prev" }

func openLog(path string, segMax int64) (*Log, error) {
	if err := trimFile(path, segMax); err != nil {
		return nil, err
	}
	if err := trimFile(prevPath(path), segMax); err != nil {
		return nil, err
	}
	cur, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	curInfo, err := cur.Stat()
	if err != nil {
		_ = cur.Close()
		return nil, err
	}
	l := &Log{path: path, cur: cur, segMax: segMax, epoch: newEpoch()}
	if prev, err := os.Open(prevPath(path)); err == nil {
		info, err := prev.Stat()
		if err != nil {
			_ = prev.Close()
			_ = cur.Close()
			return nil, err
		}
		l.prev = prev
		l.curStart = info.Size()
	}
	l.end.Store(l.curStart + curInfo.Size())
	return l, nil
}

func newEpoch() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// trimFile cuts a file down to its last max bytes. Logs written before the
// cap existed run to hundreds of MB; they are brought under it the first time
// they are opened, keeping the most recent output.
func trimFile(path string, max int64) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if max <= 0 || info.Size() <= max {
		return nil
	}
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := os.OpenFile(path+".trim", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, io.NewSectionReader(src, info.Size()-max, max))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path + ".trim")
		return fmt.Errorf("trim %s: %w", path, err)
	}
	// Keep the original modification time: it is when the session last
	// produced output, which is what log retention measures. Trimming is not
	// output, and resetting it would postpone every orphan's expiry.
	_ = os.Chtimes(path+".trim", info.ModTime(), info.ModTime())
	return os.Rename(path+".trim", path)
}

// Write appends to the newer segment, rotating first if it would pass
// segMax. Only the session's read loop writes, so the segment fields are
// read here without the lock; the lock is taken to change them.
func (l *Log) Write(p []byte) (int, error) {
	if l == nil || l.cur == nil {
		return len(p), nil // log disabled — silently accept
	}
	if size := l.end.Load() - l.curStart; size > 0 && size+int64(len(p)) > l.segMax {
		if err := l.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := l.cur.Write(p)
	if n > 0 {
		l.end.Add(int64(n))
	}
	return n, err
}

func (l *Log) rotate() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.prev != nil {
		_ = l.prev.Close()
	}
	if err := os.Rename(l.path, prevPath(l.path)); err != nil {
		return fmt.Errorf("rotate log: %w", err)
	}
	cur, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("rotate log: %w", err)
	}
	// The renamed file handle still reads the same inode, so the old newer
	// segment becomes the older one without being reopened.
	l.prev, l.cur = l.cur, cur
	l.prevStart, l.curStart = l.curStart, l.end.Load()
	return nil
}

// Base is the oldest logical offset still on disk.
func (l *Log) Base() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.prev != nil {
		return l.prevStart
	}
	return l.curStart
}

// End is the logical offset one past the last byte written.
func (l *Log) End() int64 { return l.end.Load() }

// Epoch identifies this Log value; offsets are only comparable within it.
func (l *Log) Epoch() string { return l.epoch }

// ReadAt fills p from logical offset off. It reads from one segment per
// call, so it can return fewer bytes than len(p) with a nil error; callers
// loop. Returns io.EOF at End and ErrTooOld below Base.
func (l *Log) ReadAt(p []byte, off int64) (int, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.cur == nil {
		return 0, errLogClosed
	}
	end := l.end.Load()
	base := l.curStart
	if l.prev != nil {
		base = l.prevStart
	}
	switch {
	case off < base:
		return 0, ErrTooOld
	case off >= end:
		return 0, io.EOF
	}
	f, at, limit := l.cur, off-l.curStart, end-off
	if off < l.curStart {
		f, at, limit = l.prev, off-l.prevStart, l.curStart-off
	}
	if int64(len(p)) > limit {
		p = p[:limit]
	}
	n, err := f.ReadAt(p, at)
	if errors.Is(err, io.EOF) && n > 0 {
		err = nil
	}
	return n, err
}

// readRange returns the bytes in [from, to).
func (l *Log) readRange(from, to int64) ([]byte, error) {
	out := make([]byte, to-from)
	for done := 0; done < len(out); {
		n, err := l.ReadAt(out[done:], from+int64(done))
		done += n
		if err != nil && !(errors.Is(err, io.EOF) && done == len(out)) {
			return nil, err
		}
	}
	return out, nil
}

// Snapshot is what a client is sent before it starts following live output:
// the bytes from `from` up to the current end, or, when `from` cannot be
// honoured, the last tailBytes after a terminal reset.
type Snapshot struct {
	Data     []byte // terminal queries already stripped
	Reset    bool   // client must clear its terminal before writing Data
	LiveFrom int64  // logical offset the client follows from after Data
}

// Snapshot builds the catch-up for a client that has everything before
// `from` in a log named `epoch`. An empty epoch, a different one (roost
// restarted), an offset rotated out, or a gap larger than tailBytes all fall
// back to a reset plus the tail — a resume that far back would replay more
// than a fresh attach does.
//
// Terminal queries are stripped so the client's xterm.js doesn't re-respond
// to historical queries (those responses would land in the shell's stdin
// and show up as garbage like "0;276;0c" after the prompt). Live output is
// read from the log unfiltered — actively-running programs that ask the
// terminal a question still get an answer.
func (l *Log) Snapshot(epoch string, from, tailBytes int64) (Snapshot, error) {
	if l == nil || l.cur == nil {
		return Snapshot{Reset: true}, nil
	}
	end, base := l.End(), l.Base()
	if tailBytes <= 0 {
		tailBytes = end - base
	}
	start := from
	reset := epoch != l.epoch || from < base || from > end || end-from > tailBytes
	if reset {
		start = max(base, end-tailBytes)
	}
	data, err := l.readRange(start, end)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Data: stripTerminalQueries(data), Reset: reset, LiveFrom: end}, nil
}

// queryPatterns are CSI sequences that elicit a response from a VT-style
// terminal. We strip them out of replay buffers; the shell never sees them
// on first run because they go terminal-ward, not shell-ward. On replay
// though, the *terminal* sees them again and dutifully answers, and that
// answer travels back to the shell as if typed.
var queryPatterns = [][]byte{
	{0x1b, '[', 'c'},          // DA1
	{0x1b, '[', '0', 'c'},     // DA1 (with explicit 0)
	{0x1b, '[', '>', 'c'},     // DA2
	{0x1b, '[', '=', 'c'},     // DA3
	{0x1b, '[', '>', 'q'},     // XTQUERYNAME
	{0x1b, '[', '5', 'n'},     // DSR — operating status
	{0x1b, '[', '6', 'n'},     // DSR — cursor position
	{0x1b, '[', '?', '6', 'n'}, // DEC cursor position
}

func stripTerminalQueries(b []byte) []byte {
	for _, p := range queryPatterns {
		for {
			i := bytesIndex(b, p)
			if i < 0 {
				break
			}
			b = append(b[:i], b[i+len(p):]...)
		}
	}
	return b
}

// bytesIndex is a tiny stand-in for bytes.Index to keep this file's
// import list short.
func bytesIndex(s, sub []byte) int {
	if len(sub) == 0 {
		return 0
	}
outer:
	for i := 0; i+len(sub) <= len(s); i++ {
		for j := 0; j < len(sub); j++ {
			if s[i+j] != sub[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}

// Size returns the bytes the log currently holds on disk.
func (l *Log) Size() int64 {
	if l == nil {
		return 0
	}
	return l.End() - l.Base()
}

// Rename moves both segments; open handles follow the files.
func (l *Log) Rename(newPath string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.Rename(l.path, newPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(prevPath(l.path), prevPath(newPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	l.path = newPath
	return nil
}

// Close closes the underlying files. After Close, further writes are no-ops.
func (l *Log) Close() error {
	if l == nil || l.cur == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	err := l.cur.Close()
	l.cur = nil
	if l.prev != nil {
		_ = l.prev.Close()
		l.prev = nil
	}
	return err
}

// removeLogFiles deletes both segments of the log at path.
func removeLogFiles(path string) error {
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if perr := os.Remove(prevPath(path)); perr != nil && !errors.Is(perr, os.ErrNotExist) && err == nil {
		err = perr
	}
	return err
}
