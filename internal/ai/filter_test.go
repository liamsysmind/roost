package ai

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsMetaUserMessage(t *testing.T) {
	noise := []string{
		"<local-command-caveat>Caveat:...",
		"<task-notification>\n<task-id>abc",
		"<command-name>/codex:review</command-name>",
		"<command-message>codex:review</command-message>\n<command-name>/codex:review</command-name>",
		"<command-args></command-args>",
		"[Request interrupted by user]",
		"[Request interrupted by user for tool use]",
		"codex:review",
		"claude:thinking",
		"plugin-with-dash:cmd_with_under",
		"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.\n\nSummary:\n1. Primary Request and Intent: …",
	}
	for _, c := range noise {
		if !isMetaUserMessage(c) {
			t.Errorf("expected meta: %q", c)
		}
	}

	real := []string{
		"push it",
		"commit it",
		"修那個 fs.go symlink P2",
		"開始變好用了, 進入到claude code 不同的session, 一下就可以跳到很多歷史對話",
		"please check http://example.com:8080 for me", // colon inside URL — not standalone slug:slug
		"yes",
		"",
		":bare-leading-colon",
		"bare-trailing-colon:",
		"more:than:one:colon", // multiple colons should be allowed-through (looks like a path/URL fragment, not a slash command)
	}
	for _, c := range real {
		if isMetaUserMessage(c) {
			t.Errorf("expected NOT meta: %q", c)
		}
	}
}

// TestReadActiveOriginFilter guards the regression where newer Claude Code
// tags real prompts with origin.kind="human": a blanket "any origin ⇒ skip"
// dropped every real prompt from the Activity panel. Only non-human origins
// (task-notification, etc.) should be filtered; human and origin-less prompts
// must survive, while tool_result user turns must not appear.
func TestReadActiveOriginFilter(t *testing.T) {
	lines := []string{
		// real prompt, new format (origin.kind=human)
		`{"type":"user","uuid":"u1","timestamp":"2026-07-11T10:00:00Z","origin":{"kind":"human"},"message":{"role":"user","content":"human prompt new"}}`,
		// real prompt, old format (no origin)
		`{"type":"user","uuid":"u2","timestamp":"2026-07-11T10:01:00Z","message":{"role":"user","content":"prompt old style"}}`,
		// machine-injected task notification — must be skipped
		`{"type":"user","uuid":"u3","timestamp":"2026-07-11T10:02:00Z","origin":{"kind":"task-notification"},"message":{"role":"user","content":"<task-notification>\n<task-id>abc</task-id>"}}`,
		// tool result recorded as a user turn — must be skipped
		`{"type":"user","uuid":"u4","timestamp":"2026-07-11T10:03:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"ls output"}]}}`,
		// user interrupt marker — must be skipped
		`{"type":"user","uuid":"u5","timestamp":"2026-07-11T10:04:00Z","origin":{"kind":"human"},"message":{"role":"user","content":"[Request interrupted by user]"}}`,
	}
	dir := t.TempDir()
	slug := "-tmp-proj"
	if err := os.MkdirAll(filepath.Join(dir, slug), 0o755); err != nil {
		t.Fatal(err)
	}
	file := "s.jsonl"
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, slug, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &Reader{Root: dir}
	act, err := r.readActive(slug, file, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range act.Prompts {
		got[p.Preview] = true
	}
	for _, want := range []string{"human prompt new", "prompt old style"} {
		if !got[want] {
			t.Errorf("expected prompt %q to be kept; got %v", want, act.Prompts)
		}
	}
	if len(act.Prompts) != 2 {
		t.Errorf("expected exactly 2 real prompts, got %d: %v", len(act.Prompts), act.Prompts)
	}
}
