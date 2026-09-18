package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsRolloutPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/Users/me/.codex/sessions/2026/09/16/rollout-2026-09-16T11-07-55-abc.jsonl", true},
		{"rollout-x.jsonl", true},
		{"/tmp/rollout-x.jsonl", true},
		{"/tmp/rollout-x.json", false},
		{"/tmp/notarollout.jsonl", false},
		{"/tmp/prefix-rollout-x.jsonl", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isRolloutPath(c.path); got != c.want {
			t.Errorf("isRolloutPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// With no usable pid — no agent in the pane, a process that has gone, a
// platform where neither /proc nor lsof answers — resolution has to fall back
// to the old cwd match rather than showing nothing.
func TestActiveForProcessFallsBackToCwd(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "09", "18")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const cwd = "/Users/me/project"
	body := `{"type":"session_meta","payload":{"id":"s1","cwd":"` + cwd + `"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-09-18T00:00:00Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "rollout-a.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &CodexReader{Root: root}
	for _, pid := range []int{0, -1, 999999} {
		s, err := r.ActiveForProcess(pid, cwd)
		if err != nil {
			t.Fatalf("pid %d: %v", pid, err)
		}
		if s == nil || len(s.Prompts) != 1 {
			t.Errorf("pid %d: fell back to nothing; want the cwd match", pid)
		}
	}
}

// One oversized line must not take the rest of the file with it. bufio.Scanner
// cannot resume after ErrTooLong, so before this the whole session read as an
// error and the panel showed nothing at all.
func TestOneOversizedLineKeepsTheRest(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "09", "18")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const cwd = "/Users/me/project"
	var b strings.Builder
	b.WriteString(`{"type":"session_meta","payload":{"id":"s1","cwd":"` + cwd + `"}}` + "\n")
	b.WriteString(`{"type":"response_item","timestamp":"2026-09-18T00:00:01Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"before"}]}}` + "\n")
	// Past maxJSONLLine, so the scan stops here.
	b.WriteString(`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` +
		strings.Repeat("x", maxJSONLLine+1024) + `"}]}}` + "\n")
	b.WriteString(`{"type":"response_item","timestamp":"2026-09-18T00:00:02Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"after"}]}}` + "\n")

	if err := os.WriteFile(filepath.Join(dir, "rollout-b.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := (&CodexReader{Root: root}).Active(cwd)
	if err != nil {
		t.Fatalf("Active returned an error for a file with one long line: %v", err)
	}
	if s == nil {
		t.Fatal("no session; one long line discarded the whole file")
	}
	if len(s.Prompts) == 0 {
		t.Error("no prompts survived; the lines before the long one should have")
	}
}
