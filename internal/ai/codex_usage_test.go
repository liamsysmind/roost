package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Usage.Messages used to be derived from out.Prompts, which is truncated to
// the 40 most recent for display — so every session past its fortieth turn
// reported exactly 40. The count has to come from the scan, not the slice.
func TestCodexUsageMessagesIsNotCappedByPromptLimit(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "09", "14")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	const cwd = "/Users/me/project"
	var b strings.Builder
	b.WriteString(`{"type":"session_meta","payload":{"id":"s1","cwd":"` + cwd + `"}}` + "\n")
	const turns = 57 // deliberately past the 40-prompt display cap
	for i := 0; i < turns; i++ {
		b.WriteString(`{"type":"response_item","timestamp":"2026-09-14T00:00:0` +
			string(rune('0'+i%10)) + `Z","payload":{"type":"message","role":"user",` +
			`"content":[{"type":"input_text","text":"turn"}]}}` + "\n")
	}
	b.WriteString(`{"type":"event_msg","payload":{"type":"token_count","info":{` +
		`"total_token_usage":{"input_tokens":900,"cached_input_tokens":700,"output_tokens":50},` +
		`"last_token_usage":{"input_tokens":144388,"cached_input_tokens":136192,"output_tokens":279},` +
		`"model_context_window":258400}}}` + "\n")

	if err := os.WriteFile(filepath.Join(dir, "rollout-test.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := (&CodexReader{Root: root}).Active(cwd)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if s == nil {
		t.Fatal("no active session found")
	}

	if s.Usage.Messages != turns {
		t.Errorf("Usage.Messages = %d, want %d (the prompt list is capped, the count is not)",
			s.Usage.Messages, turns)
	}
	if len(s.Prompts) != 40 {
		t.Errorf("len(Prompts) = %d, want 40 (display cap still applies)", len(s.Prompts))
	}
	// The two numbers roost shows as "context": the last turn's input, and the
	// window it is a fraction of.
	if s.ContextTokens != 144388 {
		t.Errorf("ContextTokens = %d, want 144388", s.ContextTokens)
	}
	if s.ContextWindow != 258400 {
		t.Errorf("ContextWindow = %d, want 258400", s.ContextWindow)
	}
}
