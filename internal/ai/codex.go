// Codex (OpenAI codex-tui) stores session "rollout" files at
// ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl. Each file's first line is a
// session_meta record with the cwd at launch; later lines are response_item
// (messages) and event_msg (token counts, etc.) entries. We resolve the
// active rollout for a terminal's cwd by scanning recent rollouts, reading
// each session_meta, and picking the most recently modified one whose cwd
// matches.

package ai

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CodexReader resolves the active Codex rollout file for a given cwd.
type CodexReader struct {
	Root string // typically ~/.codex/sessions
}

// NewCodexReader constructs a reader rooted at $HOME/.codex/sessions.
func NewCodexReader() *CodexReader {
	home, _ := os.UserHomeDir()
	if home == "" {
		return &CodexReader{}
	}
	return &CodexReader{Root: filepath.Join(home, ".codex", "sessions")}
}

type codexSessionMeta struct {
	Type    string `json:"type"` // "session_meta"
	Payload struct {
		ID            string `json:"id"`
		Cwd           string `json:"cwd"`
		Model         string `json:"model"`
		ModelProvider string `json:"model_provider"`
		Originator    string `json:"originator"`
	} `json:"payload"`
}

type codexResponseItem struct {
	Type    string `json:"type"` // "response_item"
	Payload struct {
		Type    string          `json:"type"` // "message"
		Role    string          `json:"role"` // "user" / "assistant"
		Content json.RawMessage `json:"content"`
	} `json:"payload"`
	Timestamp string `json:"timestamp"`
}

// Active returns the most recent rollout for the given cwd, with its user
// prompts extracted. Returns nil if Codex isn't installed or no rollout
// matches the cwd.
func (r *CodexReader) Active(cwd string) (*ActiveSession, error) {
	if r.Root == "" {
		return nil, nil
	}
	files, err := r.recentRollouts(48 * time.Hour)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	// Most-recent-first; pick the first one whose session_meta.cwd matches.
	for _, fi := range files {
		meta, err := readCodexMeta(fi.path)
		if err != nil || meta == nil {
			continue
		}
		if meta.Payload.Cwd != cwd {
			continue
		}
		return r.readActive(fi.path, fi.mtime, meta)
	}
	return nil, nil
}

type rolloutFile struct {
	path  string
	mtime time.Time
}

// recentRollouts returns rollout files modified within `since`, sorted newest
// first. Walks `~/.codex/sessions/YYYY/MM/DD/`.
func (r *CodexReader) recentRollouts(since time.Duration) ([]rolloutFile, error) {
	cutoff := time.Now().Add(-since)
	var out []rolloutFile
	err := filepath.WalkDir(r.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Permission errors etc. — skip subtree but keep walking.
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasPrefix(d.Name(), "rollout-") || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			return nil
		}
		out = append(out, rolloutFile{path: path, mtime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].mtime.After(out[j].mtime) })
	return out, nil
}

func readCodexMeta(path string) (*codexSessionMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	if !sc.Scan() {
		return nil, errors.New("empty rollout")
	}
	var meta codexSessionMeta
	if err := json.Unmarshal(sc.Bytes(), &meta); err != nil {
		return nil, err
	}
	if meta.Type != "session_meta" {
		return nil, errors.New("first line is not session_meta")
	}
	return &meta, nil
}

func (r *CodexReader) readActive(path string, mtime time.Time, meta *codexSessionMeta) (*ActiveSession, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	model := meta.Payload.Model
	if model == "" {
		model = "codex" // fallback: rollouts don't always record a specific model name in session_meta
	}
	out := &ActiveSession{
		Project:  meta.Payload.Cwd,
		Slug:     meta.Payload.ID,
		File:     filepath.Base(path),
		Modified: mtime,
		Model:    model,
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<22)
	for sc.Scan() {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(sc.Bytes(), &probe); err != nil {
			continue
		}
		if probe.Type != "response_item" {
			continue
		}
		var item codexResponseItem
		if err := json.Unmarshal(sc.Bytes(), &item); err != nil {
			continue
		}
		if item.Payload.Type != "message" || item.Payload.Role != "user" {
			continue
		}
		text := extractCodexUserText(item.Payload.Content)
		if text == "" || isCodexEnvironmentBlock(text) {
			continue
		}
		preview := text
		if len(preview) > 140 {
			preview = preview[:140] + "…"
		}
		out.Prompts = append(out.Prompts, PromptEntry{
			Timestamp: item.Timestamp,
			Preview:   preview,
		})
	}
	sort.SliceStable(out.Prompts, func(i, j int) bool {
		return out.Prompts[i].Timestamp > out.Prompts[j].Timestamp
	})
	if len(out.Prompts) > 40 {
		out.Prompts = out.Prompts[:40]
	}
	return out, sc.Err()
}

// extractCodexUserText pulls a single text string out of Codex's content
// array. Each entry may have shape {type: "input_text", text: "..."} or just
// be a string. We concatenate text parts with newlines.
func extractCodexUserText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Plain string content.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	// Array of content parts.
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(p.Text)
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

// isCodexEnvironmentBlock skips Codex's auto-injected <environment_context>
// blocks, which look like user messages in the JSONL but are not actual user
// input.
func isCodexEnvironmentBlock(text string) bool {
	t := strings.TrimLeft(text, " \t\r\n")
	return strings.HasPrefix(t, "<environment_context>")
}
