package session

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// writeAll appends each chunk and returns everything written, in order.
func writeAll(t *testing.T, l *Log, chunks ...string) []byte {
	t.Helper()
	var all []byte
	for _, c := range chunks {
		if _, err := l.Write([]byte(c)); err != nil {
			t.Fatalf("write %q: %v", c, err)
		}
		all = append(all, c...)
	}
	return all
}

// The disk cap is the point of the two segments: once rotation has happened
// the log must hold at most two segments' worth, and every offset still on
// disk must read back the byte that was written there.
func TestLogRotationKeepsOffsetsAndCap(t *testing.T) {
	cases := []struct {
		name     string
		segMax   int64
		chunks   []string
		wantBase int64
	}{
		{"no rotation", 10, []string{"abc", "def"}, 0},
		{"one rotation", 10, []string{"0123456", "789ab"}, 0},
		{"older segment deleted", 10, []string{"0123456", "789ab", "cdefghij", "k"}, 7},
		{"chunk larger than segment", 4, []string{"ab", "cdefghij", "kl"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.log")
			l, err := openLog(path, tc.segMax)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			all := writeAll(t, l, tc.chunks...)

			if got := l.End(); got != int64(len(all)) {
				t.Fatalf("End = %d, want %d", got, len(all))
			}
			if got := l.Base(); got != tc.wantBase {
				t.Fatalf("Base = %d, want %d", got, tc.wantBase)
			}
			got, err := l.readRange(l.Base(), l.End())
			if err != nil {
				t.Fatal(err)
			}
			if want := all[l.Base():]; !bytes.Equal(got, want) {
				t.Fatalf("read %q, want %q", got, want)
			}
			if l.Base() > 0 {
				if _, err := l.ReadAt(make([]byte, 1), l.Base()-1); !errors.Is(err, ErrTooOld) {
					t.Fatalf("ReadAt below Base: err = %v, want ErrTooOld", err)
				}
			}
			if _, err := l.ReadAt(make([]byte, 1), l.End()); !errors.Is(err, io.EOF) {
				t.Fatalf("ReadAt at End: err = %v, want EOF", err)
			}
		})
	}
}

// Logs from before the cap existed are hundreds of MB. Opening one must cut it
// to its most recent output, not refuse it or keep it whole.
func TestOpenLogTrimsOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.log")
	if err := os.WriteFile(path, []byte("old-old-old-RECENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prevPath(path), []byte("ancient-ancient"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := openLog(path, 6)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got, err := l.readRange(l.Base(), l.End())
	if err != nil {
		t.Fatal(err)
	}
	if want := "ncientRECENT"; string(got) != want {
		t.Fatalf("after trim read %q, want %q", got, want)
	}
}

// Snapshot decides between resuming and starting over. Resuming at the wrong
// place would splice two unrelated byte streams into one terminal, so every
// position the client cannot vouch for must fall back to reset + tail.
func TestLogSnapshotResumeOrReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.log")
	l, err := openLog(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	writeAll(t, l, "0123456", "789ab", "cdefghij") // End 20, Base 7

	cases := []struct {
		name      string
		epoch     string
		from      int64
		tail      int64
		wantReset bool
		wantData  string
	}{
		{"resume mid-log", l.Epoch(), 15, 8, false, "fghij"},
		{"resume at end", l.Epoch(), 20, 8, false, ""},
		{"fresh tab", "", 0, 8, true, "cdefghij"},
		{"other epoch", "stale", 15, 8, true, "cdefghij"},
		{"rotated out", l.Epoch(), 3, 20, true, "789abcdefghij"},
		{"gap larger than tail", l.Epoch(), 9, 4, true, "ghij"},
		{"ahead of end", l.Epoch(), 25, 8, true, "cdefghij"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := l.Snapshot(tc.epoch, tc.from, tc.tail)
			if err != nil {
				t.Fatal(err)
			}
			if snap.Reset != tc.wantReset || string(snap.Data) != tc.wantData || snap.LiveFrom != 20 {
				t.Fatalf("got reset=%v data=%q live=%d, want reset=%v data=%q live=20",
					snap.Reset, snap.Data, snap.LiveFrom, tc.wantReset, tc.wantData)
			}
		})
	}
}

func TestLogRenameAndRemoveCoverBothSegments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	l, err := openLog(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	writeAll(t, l, "abcd", "efgh")
	newPath := filepath.Join(dir, "b.log")
	if err := l.Rename(newPath); err != nil {
		t.Fatal(err)
	}
	writeAll(t, l, "ij") // the open handle still writes to the moved file
	_ = l.Close()
	for _, p := range []string{newPath, prevPath(newPath)} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s missing after rename: %v", filepath.Base(p), err)
		}
	}
	if err := removeLogFiles(newPath); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files left after remove: %v", entries)
	}
}
