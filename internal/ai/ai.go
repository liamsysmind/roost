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
	ContextTokens    int64         `json:"context_tokens"`     // input + cache_read + cache_creation on the latest assistant turn
	ContextWindowEst int64         `json:"context_window_est"` // 200K default, bumped to 1M when overflowed
	Prompts          []PromptEntry `json:"prompts"`
}

// PromptEntry summarises one user message.
type PromptEntry struct {
	UUID      string `json:"uuid"`
	Timestamp string `json:"timestamp"`
	Preview   string `json:"preview"`
}

// Active scans every session jsonl, picks the most recently modified one,
// and returns the model / context / prompts view.
func (r *Reader) Active() (*ActiveSession, error) {
	if r.Root == "" {
		return nil, nil
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

func (r *Reader) readActive(slug, file string, mtime time.Time) (*ActiveSession, error) {
	path := filepath.Join(r.Root, slug, file)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := &ActiveSession{
		Project:          slugToProject(slug),
		Slug:             slug,
		File:             file,
		Modified:         mtime,
		ContextWindowEst: 200_000,
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
			var m userMessage
			if err := json.Unmarshal(*ev.Message, &m); err != nil {
				continue
			}
			if m.Role != "user" {
				continue
			}
			content := extractContentText(m.Content)
			if content == "" {
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
	if out.ContextTokens > out.ContextWindowEst {
		out.ContextWindowEst = 1_000_000
	}
	sort.SliceStable(out.Prompts, func(i, j int) bool {
		return out.Prompts[i].Timestamp > out.Prompts[j].Timestamp
	})
	if len(out.Prompts) > 40 {
		out.Prompts = out.Prompts[:40]
	}
	return out, sc.Err()
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
