package ai

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The slug encoding replaces '/' with '-', so decoding it back is ambiguous
// for any directory whose own name contains a hyphen. These are the cases
// projectPath exists to rescue.
func TestSlugToProjectIsLossy(t *testing.T) {
	cases := []struct {
		slug, want string
	}{
		{"-Users-me-roost", "/Users/me/roost"},
		{"-Users-me-ppt-present", "/Users/me/ppt/present"},           // wrong on purpose
		{"-Users-me-roundbase-mobile", "/Users/me/roundbase/mobile"}, // wrong on purpose
		{"relative-name", "relative-name"},
	}
	for _, c := range cases {
		if got := slugToProject(c.slug); got != c.want {
			t.Errorf("slugToProject(%q) = %q, want %q", c.slug, got, c.want)
		}
	}
}

func TestCwdFromFile(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, body, want string
	}{
		{
			// The first lines of a real transcript are session metadata with
			// no cwd; the scan must not stop at line one.
			name: "after metadata lines",
			body: `{"type":"last-prompt","sessionId":"x"}
{"type":"mode","sessionId":"x"}
{"type":"user","cwd":"/Users/me/ppt-present","uuid":"u1"}
`,
			want: "/Users/me/ppt-present",
		},
		{name: "no cwd anywhere", body: "{\"type\":\"mode\"}\n", want: ""},
		{name: "empty file", body: "", want: ""},
		{
			name: "skips malformed lines",
			body: "not json\n{\"type\":\"user\",\"cwd\":\"/tmp/x\"}\n",
			want: "/tmp/x",
		},
		{
			name: "hyphenated path survives verbatim",
			body: "{\"type\":\"user\",\"cwd\":\"/Users/me/line-bot\"}\n",
			want: "/Users/me/line-bot",
		},
	}
	for _, c := range cases {
		p := filepath.Join(dir, c.name+".jsonl")
		if err := os.WriteFile(p, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := cwdFromFile(p); got != c.want {
			t.Errorf("%s: cwdFromFile = %q, want %q", c.name, got, c.want)
		}
	}
	if got := cwdFromFile(filepath.Join(dir, "missing.jsonl")); got != "" {
		t.Errorf("missing file: got %q, want %q", got, "")
	}
}

func TestProjectPath(t *testing.T) {
	root := t.TempDir()
	slug := "-Users-me-ppt-present"

	// No directory at all: fall back to the lossy decode.
	r := &Reader{Root: root}
	if got, want := r.projectPath(slug), "/Users/me/ppt/present"; got != want {
		t.Errorf("absent dir: got %q, want %q", got, want)
	}

	dir := filepath.Join(root, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Directory with no jsonl (memory-only projects look like this).
	if err := os.MkdirAll(filepath.Join(dir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := r.projectPath(slug), "/Users/me/ppt/present"; got != want {
		t.Errorf("no jsonl: got %q, want %q", got, want)
	}

	// The newest file wins, because an older one may predate the cwd field.
	old := filepath.Join(dir, "old.jsonl")
	if err := os.WriteFile(old, []byte("{\"type\":\"mode\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	newf := filepath.Join(dir, "new.jsonl")
	body := "{\"type\":\"user\",\"cwd\":\"/Users/me/ppt-present\"}\n"
	if err := os.WriteFile(newf, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := r.projectPath(slug), "/Users/me/ppt-present"; got != want {
		t.Errorf("with cwd: got %q, want %q", got, want)
	}
}
