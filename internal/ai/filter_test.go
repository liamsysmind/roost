package ai

import "testing"

func TestIsMetaUserMessage(t *testing.T) {
	noise := []string{
		"<local-command-caveat>Caveat:...",
		"<task-notification>\n<task-id>abc",
		"<command-name>/codex:review</command-name>",
		"<command-message>codex:review</command-message>\n<command-name>/codex:review</command-name>",
		"<command-args></command-args>",
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
