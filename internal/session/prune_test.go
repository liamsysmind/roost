package session

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// Pruning deletes output nobody can get back, so the rule has to hold both
// ways: an ended session's old log goes, and nothing whose shell may still
// hold work does, however old its output.
func TestStaleLogsOnlyEndedAndOld(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-30 * 24 * time.Hour)
	old, recent := now.Add(-40*24*time.Hour), now.Add(-2*24*time.Hour)

	cases := []struct {
		name  string
		files map[string]time.Time // filename → mtime
		alive []string
		want  []string
	}{
		{"ended and old is pruned",
			map[string]time.Time{"gone.log": old}, nil, []string{"gone"}},
		{"ended but recent is kept",
			map[string]time.Time{"fresh.log": recent}, nil, nil},
		{"alive is kept however old",
			map[string]time.Time{"running.log": old}, []string{"running"}, nil},
		{"recent newer segment keeps an old older segment",
			map[string]time.Time{"split.log": recent, "split.log.prev": old}, nil, nil},
		{"both segments old are pruned together",
			map[string]time.Time{"both.log": old, "both.log.prev": old}, nil, []string{"both"}},
		{"orphan older segment alone is pruned",
			map[string]time.Time{"lone.log.prev": old}, nil, []string{"lone"}},
		{"other files are ignored",
			map[string]time.Time{"notes.txt": old, "x.log.trim": old}, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, mt := range tc.files {
				p := filepath.Join(dir, name)
				if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(p, mt, mt); err != nil {
					t.Fatal(err)
				}
			}
			alive := func(id string) bool {
				for _, a := range tc.alive {
					if a == id {
						return true
					}
				}
				return false
			}
			var got []string
			for _, d := range staleLogs(dir, cutoff, alive) {
				got = append(got, d.id)
			}
			sort.Strings(got)
			if len(got) != len(tc.want) || (len(got) > 0 && got[0] != tc.want[0]) {
				t.Fatalf("stale = %v, want %v", got, tc.want)
			}
		})
	}
}

// Trimming is not output. If it reset the modification time, the first start
// after the size cap arrived would make every old log look fresh and postpone
// its pruning by a full retention period.
func TestTrimKeepsModificationTime(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.log")
	if err := os.WriteFile(p, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	if err := trimFile(p, 4); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 4 || !info.ModTime().Equal(old) {
		t.Fatalf("after trim size=%d mtime=%s, want 4 and %s", info.Size(), info.ModTime(), old)
	}
}
