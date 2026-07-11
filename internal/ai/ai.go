// Package ai reads Claude Code state files and surfaces usage metadata.
//
// Claude Code stores conversations under ~/.claude/projects/{slug}/{uuid}.jsonl
// where {slug} is the working directory with separators replaced by '-'.
// Each line is a JSON event; assistant messages carry a "usage" object
// with token counts that we aggregate.
//
// We deliberately do NOT compute USD cost: API prices shift, subscription
// plans (Claude Max/Pro) bill differently from raw API, and a stale local
// table is worse than no number. Tokens are objective; dollars are not.
package ai

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Reader scans Claude Code state files.
type Reader struct {
	Root string // typically ~/.claude/projects
}

func NewReader() *Reader {
	home, _ := os.UserHomeDir()
	return &Reader{Root: filepath.Join(home, ".claude", "projects")}
}

// Usage is an aggregated token tally for one or more JSONL files.
type Usage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	CacheWrite5m     int64 `json:"cache_write_5m_tokens"`
	CacheWrite1h     int64 `json:"cache_write_1h_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	Messages         int   `json:"messages"`
}

// SessionInfo summarises one project session file.
type SessionInfo struct {
	Project  string    `json:"project"`
	Slug     string    `json:"slug"`
	File     string    `json:"file"`
	Modified time.Time `json:"modified"`
	Usage    Usage     `json:"usage"`
}

// rawEvent only decodes the few fields we care about.
type rawEvent struct {
	Type      string           `json:"type"`
	UUID      string           `json:"uuid"`
	Message   *json.RawMessage `json:"message"`
	Timestamp string           `json:"timestamp"`
	// IsMeta flags entries that aren't real user input (slash-command body
	// templates, etc.) — Claude Code sets this on programmatically-generated
	// "user" messages.
	IsMeta bool `json:"isMeta"`
	// IsCompactSummary flags the auto-injected "This session is being
	// continued from a previous conversation…" body that Claude Code writes
	// after a context-window compaction. It's structurally a user message but
	// not user input.
	IsCompactSummary bool `json:"isCompactSummary"`
	// Origin labels the source of a message. Newer Claude Code tags real
	// user input with {"kind":"human"} and machine-injected messages with
	// other kinds (e.g. "task-notification" for background-task notices);
	// older versions omit origin entirely on real prompts.
	Origin *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
}

// assistantMessage is the shape we care about when type == "assistant".
type assistantMessage struct {
	Model string `json:"model"`
	Usage *struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreation            *struct {
			Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
			Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
		} `json:"cache_creation"`
	} `json:"usage"`
}

// userMessage is the shape we care about when type == "user".
type userMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// readFile aggregates tokens from one JSONL file, optionally filtered by a
// "since" cutoff applied to event timestamps when present.
func readFile(path string, since time.Time) (Usage, error) {
	var u Usage
	f, err := os.Open(path)
	if err != nil {
		return u, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<22)
	for sc.Scan() {
		var ev rawEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Type != "assistant" || ev.Message == nil {
			continue
		}
		var msg assistantMessage
		if err := json.Unmarshal(*ev.Message, &msg); err != nil || msg.Usage == nil {
			continue
		}
		if !since.IsZero() && ev.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil && t.Before(since) {
				continue
			}
		}
		usg := msg.Usage
		u.InputTokens += usg.InputTokens
		u.OutputTokens += usg.OutputTokens
		u.CacheWriteTokens += usg.CacheCreationInputTokens
		u.CacheReadTokens += usg.CacheReadInputTokens
		if usg.CacheCreation != nil {
			u.CacheWrite5m += usg.CacheCreation.Ephemeral5mInputTokens
			u.CacheWrite1h += usg.CacheCreation.Ephemeral1hInputTokens
		} else {
			u.CacheWrite5m += usg.CacheCreationInputTokens
		}
		u.Messages++
	}
	return u, sc.Err()
}

// Total walks every project session file and returns the aggregated tokens.
func (r *Reader) Total(since time.Time) (Usage, error) {
	var total Usage
	if r.Root == "" {
		return total, nil
	}
	projs, err := os.ReadDir(r.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return total, nil
		}
		return total, err
	}
	for _, p := range projs {
		if !p.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(r.Root, p.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			u, err := readFile(filepath.Join(r.Root, p.Name(), f.Name()), since)
			if err != nil {
				continue
			}
			total.InputTokens += u.InputTokens
			total.OutputTokens += u.OutputTokens
			total.CacheWriteTokens += u.CacheWriteTokens
			total.CacheWrite5m += u.CacheWrite5m
			total.CacheWrite1h += u.CacheWrite1h
			total.CacheReadTokens += u.CacheReadTokens
			total.Messages += u.Messages
		}
	}
	return total, nil
}

// Sessions returns one entry per session file, sorted by modification time
// descending. recent caps the result to the N most recent.
func (r *Reader) Sessions(since time.Time, limit int) ([]SessionInfo, error) {
	out := []SessionInfo{}
	if r.Root == "" {
		return out, nil
	}
	projs, err := os.ReadDir(r.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	for _, p := range projs {
		if !p.IsDir() {
			continue
		}
		slug := p.Name()
		project := slugToProject(slug)
		files, err := os.ReadDir(filepath.Join(r.Root, slug))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			full := filepath.Join(r.Root, slug, f.Name())
			u, _ := readFile(full, since)
			if u.Messages == 0 && !since.IsZero() {
				continue
			}
			out = append(out, SessionInfo{
				Project:  project,
				Slug:     slug,
				File:     f.Name(),
				Modified: info.ModTime(),
				Usage:    u,
			})
		}
	}
	sortByModifiedDesc(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func slugToProject(slug string) string {
	if strings.HasPrefix(slug, "-") {
		return "/" + strings.ReplaceAll(slug[1:], "-", "/")
	}
	return slug
}

func sortByModifiedDesc(s []SessionInfo) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1].Modified.Before(s[j].Modified); j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// ActiveSession is the live context view of one session: model in use,
// rolling context-window estimate, and the user prompts so far.
type ActiveSession struct {
	Project          string        `json:"project"`
	Slug             string        `json:"slug"`
	File             string        `json:"file"`
	Modified         time.Time     `json:"modified"`
	Model            string        `json:"model"`
	Usage            Usage         `json:"usage"`
	ContextTokens int64         `json:"context_tokens"` // input + cache_read + cache_creation on the latest assistant turn
	Prompts       []PromptEntry `json:"prompts"`
}

// PromptEntry summarises one user message.
type PromptEntry struct {
	UUID      string `json:"uuid"`
	Timestamp string `json:"timestamp"`
	Preview   string `json:"preview"`
}

// Active returns the live Claude Code session view.
//
//   - hintCwd != "":  only look at the jsonl directory that matches that
//                     working directory. If nothing is there, return nil
//                     (no Claude session for this project).
//   - hintCwd == "":  scan every project and return the most recently
//                     modified jsonl across the lot.
func (r *Reader) Active(hintCwd string) (*ActiveSession, error) {
	if r.Root == "" {
		return nil, nil
	}
	if hintCwd != "" {
		return r.activeForCwd(hintCwd)
	}
	projs, err := os.ReadDir(r.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var best struct {
		slug  string
		file  string
		mtime time.Time
	}
	for _, p := range projs {
		if !p.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(r.Root, p.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			if info.ModTime().After(best.mtime) {
				best.slug = p.Name()
				best.file = f.Name()
				best.mtime = info.ModTime()
			}
		}
	}
	if best.file == "" {
		return nil, nil
	}
	return r.readActive(best.slug, best.file, best.mtime)
}

// projectToSlug mirrors Claude Code's slugification of cwd into its
// projects/ directory name. "/home/alice/work/zephyr" becomes
// "-home-alice-work-zephyr".
func projectToSlug(cwd string) string {
	if cwd == "" {
		return ""
	}
	if strings.HasPrefix(cwd, "/") {
		return "-" + strings.ReplaceAll(cwd[1:], "/", "-")
	}
	return strings.ReplaceAll(cwd, "/", "-")
}

// activeForCwd looks only inside the slug directory that maps from cwd.
// Returns nil if no jsonl exists there (caller surfaces "no AI session for
// this project" rather than falling back to global noise).
func (r *Reader) activeForCwd(cwd string) (*ActiveSession, error) {
	slug := projectToSlug(cwd)
	if slug == "" {
		return nil, nil
	}
	dir := filepath.Join(r.Root, slug)
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var best struct {
		file  string
		mtime time.Time
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(best.mtime) {
			best.file = f.Name()
			best.mtime = info.ModTime()
		}
	}
	if best.file == "" {
		return nil, nil
	}
	return r.readActive(slug, best.file, best.mtime)
}

func (r *Reader) readActive(slug, file string, mtime time.Time) (*ActiveSession, error) {
	path := filepath.Join(r.Root, slug, file)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := &ActiveSession{
		Project:  slugToProject(slug),
		Slug:     slug,
		File:     file,
		Modified: mtime,
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<22)
	for sc.Scan() {
		var ev rawEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			if ev.Message == nil {
				continue
			}
			var m assistantMessage
			if err := json.Unmarshal(*ev.Message, &m); err != nil || m.Usage == nil {
				continue
			}
			out.Model = m.Model
			u := m.Usage
			out.Usage.InputTokens += u.InputTokens
			out.Usage.OutputTokens += u.OutputTokens
			out.Usage.CacheWriteTokens += u.CacheCreationInputTokens
			out.Usage.CacheReadTokens += u.CacheReadInputTokens
			if u.CacheCreation != nil {
				out.Usage.CacheWrite5m += u.CacheCreation.Ephemeral5mInputTokens
				out.Usage.CacheWrite1h += u.CacheCreation.Ephemeral1hInputTokens
			} else {
				out.Usage.CacheWrite5m += u.CacheCreationInputTokens
			}
			out.Usage.Messages++
			out.ContextTokens = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens

		case "user":
			if ev.Message == nil {
				continue
			}
			// Skip programmatically-generated "user" entries: slash-command
			// body templates carry isMeta=true, task-completion notices carry
			// origin.kind="task-notification", and post-compaction continuation
			// bodies carry isCompactSummary=true. None is a real prompt.
			if ev.IsMeta || ev.IsCompactSummary {
				continue
			}
			// Newer Claude Code tags real prompts with origin.kind="human" and
			// machine-injected messages with other kinds; older builds set no
			// origin on real prompts. So skip only non-human origins — a blanket
			// "any origin ⇒ skip" now drops every real prompt.
			if ev.Origin != nil && ev.Origin.Kind != "" && ev.Origin.Kind != "human" {
				continue
			}
			var m userMessage
			if err := json.Unmarshal(*ev.Message, &m); err != nil {
				continue
			}
			if m.Role != "user" {
				continue
			}
			content := extractContentText(m.Content)
			if content == "" || isMetaUserMessage(content) {
				continue
			}
			preview := content
			if len(preview) > 140 {
				preview = preview[:140] + "…"
			}
			out.Prompts = append(out.Prompts, PromptEntry{
				UUID:      ev.UUID,
				Timestamp: ev.Timestamp,
				Preview:   preview,
			})
		}
	}
	sort.SliceStable(out.Prompts, func(i, j int) bool {
		return out.Prompts[i].Timestamp > out.Prompts[j].Timestamp
	})
	if len(out.Prompts) > 40 {
		out.Prompts = out.Prompts[:40]
	}
	return out, sc.Err()
}

// isMetaUserMessage reports whether a "user" JSONL entry's content is actually
// Claude Code's local-command machinery (slash-command stdin/stdout, exit
// caveat, task notifications, etc.) rather than a real user-typed prompt.
// These show up in the JSONL with type=user but should never be presented as
// prompts. The top-level isMeta / origin filters in the caller catch the
// structured cases; this is the content-based net for the rest.
func isMetaUserMessage(content string) bool {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "<local-command-") ||
		strings.HasPrefix(trimmed, "<task-notification>") {
		return true
	}
	if strings.HasPrefix(trimmed, "<command-name>") ||
		strings.HasPrefix(trimmed, "<command-message>") ||
		strings.HasPrefix(trimmed, "<command-args>") {
		return true
	}
	// Claude Code records a user-triggered interrupt as a synthetic user
	// message; it isn't a typed prompt.
	if strings.HasPrefix(trimmed, "[Request interrupted by user") {
		return true
	}
	// Post-compaction continuation body. The top-level isCompactSummary flag
	// is the primary filter; this is the content-based net in case Claude
	// Code ever emits the body without the flag set.
	if strings.HasPrefix(trimmed, "This session is being continued from a previous conversation") {
		return true
	}
	// Bare slash-command name like "codex:review" — Claude Code records the
	// invocation as a standalone user message in this form. We match
	// "word:word" with no whitespace and no other punctuation, which catches
	// the plugin:command convention without intercepting normal sentences
	// that happen to contain a colon.
	if isBareSlashCommandName(trimmed) {
		return true
	}
	return false
}

func isBareSlashCommandName(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	colon := -1
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z',
			c >= '0' && c <= '9',
			c == '-', c == '_':
			// allowed slug char
		case c == ':':
			if colon != -1 {
				return false // only one colon allowed
			}
			colon = i
		default:
			return false
		}
	}
	if colon <= 0 || colon == len(s)-1 {
		return false
	}
	// Reject leading or trailing colon, and the segments either side must be
	// non-empty by virtue of the index check above.
	return true
}

func extractContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, blk := range blocks {
			if blk.Type == "text" && blk.Text != "" {
				if b.Len() > 0 {
					b.WriteByte(' ')
				}
				b.WriteString(blk.Text)
			}
		}
		return b.String()
	}
	return ""
}
