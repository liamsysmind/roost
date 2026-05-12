// Package ai reads Claude Code state files and surfaces usage / cost.
//
// Claude Code stores conversations under ~/.claude/projects/{slug}/{uuid}.jsonl
// where {slug} is the working directory with separators replaced by '-'.
// Each line is a JSON event; assistant messages carry a "usage" object
// with token counts that we sum and price using a static pricing table.
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

// Pricing in USD per million tokens, "standard tier" rates as of the
// Claude 4 family. The cost calculator multiplies by 2× when a single
// turn's context exceeds 200K (long-context tier) on models that support
// 1M context (Opus / Sonnet).
type Pricing struct {
	Input          float64
	Output         float64
	CacheWrite5m   float64
	CacheWrite1h   float64
	CacheRead      float64
	HasLongContext bool // 200K+ turns billed at 2×
}

// modelPricing returns the price table for a model, falling back to Opus 4
// for unknown models (overestimate is safer than zero).
func modelPricing(model string) Pricing {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "haiku"):
		return Pricing{Input: 0.80, Output: 4.00, CacheWrite5m: 1.00, CacheWrite1h: 1.60, CacheRead: 0.08, HasLongContext: false}
	case strings.Contains(m, "sonnet"):
		return Pricing{Input: 3.00, Output: 15.00, CacheWrite5m: 3.75, CacheWrite1h: 6.00, CacheRead: 0.30, HasLongContext: true}
	case strings.Contains(m, "opus"):
		return Pricing{Input: 15.00, Output: 75.00, CacheWrite5m: 18.75, CacheWrite1h: 30.00, CacheRead: 1.50, HasLongContext: true}
	default:
		return Pricing{Input: 15.00, Output: 75.00, CacheWrite5m: 18.75, CacheWrite1h: 30.00, CacheRead: 1.50, HasLongContext: true}
	}
}

// longContextThreshold marks the boundary between "standard" and "long"
// pricing tiers (per-turn input >= this triggers the 2× multiplier).
const longContextThreshold = 200_000

// Reader scans Claude Code state files.
type Reader struct {
	Root string // typically ~/.claude/projects
}

func NewReader() *Reader {
	home, _ := os.UserHomeDir()
	return &Reader{Root: filepath.Join(home, ".claude", "projects")}
}

// Usage is an aggregated tally for one or more JSONL files.
type Usage struct {
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CacheWriteTokens  int64   `json:"cache_write_tokens"`
	CacheWrite5m      int64   `json:"cache_write_5m_tokens"`
	CacheWrite1h      int64   `json:"cache_write_1h_tokens"`
	CacheReadTokens   int64   `json:"cache_read_tokens"`
	Messages          int     `json:"messages"`
	LongContextTurns  int     `json:"long_context_turns"`
	Cost              float64 `json:"cost_usd"`
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
	Type    string          `json:"type"`
	UUID    string          `json:"uuid"`
	Message *json.RawMessage `json:"message"`
	Timestamp string        `json:"timestamp"`
}

// assistantMessage is the shape we care about when type == "assistant".
// CacheCreation breaks the cache-write total into 5-minute and 1-hour
// tiers (different prices). Older messages may not include it; we fall
// back to treating CacheCreationInputTokens as all-5m in that case.
type assistantMessage struct {
	Model string `json:"model"`
	Usage *struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreation *struct {
			Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
			Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
		} `json:"cache_creation"`
		ServiceTier string `json:"service_tier"`
	} `json:"usage"`
}

// messageCost prices a single assistant turn, accounting for cache-write
// tier (5m vs 1h) and long-context (200K+) tier multiplier.
func messageCost(u *struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreation *struct {
		Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	ServiceTier string `json:"service_tier"`
}, p Pricing) (cost float64, longContext bool) {
	var write5m, write1h int64
	if u.CacheCreation != nil {
		write5m = u.CacheCreation.Ephemeral5mInputTokens
		write1h = u.CacheCreation.Ephemeral1hInputTokens
	} else {
		write5m = u.CacheCreationInputTokens
	}
	mult := 1.0
	contextIn := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	if p.HasLongContext && contextIn > longContextThreshold {
		mult = 2.0
		longContext = true
	}
	cost = (float64(u.InputTokens)*p.Input +
		float64(u.OutputTokens)*p.Output +
		float64(write5m)*p.CacheWrite5m +
		float64(write1h)*p.CacheWrite1h +
		float64(u.CacheReadInputTokens)*p.CacheRead) / 1e6 * mult
	return
}

// userMessage is the shape we care about when type == "user".
// message.content can be a string OR an array of content blocks; we accept
// either via a custom Unmarshal.
type userMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// readFile aggregates usage from one JSONL file, optionally filtered by a
// "since" cutoff applied to event timestamps when present.
func readFile(path string, since time.Time) (Usage, error) {
	var u Usage
	f, err := os.Open(path)
	if err != nil {
		return u, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<22) // tolerate large lines
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
		p := modelPricing(msg.Model)
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
		cost, longCtx := messageCost(usg, p)
		u.Cost += cost
		if longCtx {
			u.LongContextTurns++
		}
	}
	return u, sc.Err()
}

// ActiveSession is the live context view of one session: model in use,
// rolling context-window estimate, and the user prompts so far.
type ActiveSession struct {
	Project           string        `json:"project"`
	Slug              string        `json:"slug"`
	File              string        `json:"file"`
	Modified          time.Time     `json:"modified"`
	Model             string        `json:"model"`
	Usage             Usage         `json:"usage"`
	ContextTokens     int64         `json:"context_tokens"`      // input + cache_read + cache_creation on the latest assistant turn
	ContextWindowEst  int64         `json:"context_window_est"`  // 200K for current Claude 4 family
	Prompts           []PromptEntry `json:"prompts"`
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
		slug string
		file string
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
		Project:  slugToProject(slug),
		Slug:     slug,
		File:     file,
		Modified: mtime,
		// Will be bumped to 1M further down if the conversation has already
		// overflowed the 200K tier. The JSONL doesn't record the [1m] suffix
		// so we infer from observed usage.
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
			p := modelPricing(m.Model)
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
			cost, longCtx := messageCost(u, p)
			out.Usage.Cost += cost
			if longCtx {
				out.Usage.LongContextTurns++
			}
			// Latest assistant message's per-turn usage is the best proxy
			// for the model's current context view.
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
	// Bump window estimate up to 1M if we've already exceeded 200K — this
	// is how we infer that the user is on the [1m]-context variant without
	// the JSONL telling us so explicitly.
	if out.ContextTokens > out.ContextWindowEst {
		out.ContextWindowEst = 1_000_000
	}
	// Sort prompts by timestamp descending (newest first).
	sort.SliceStable(out.Prompts, func(i, j int) bool {
		return out.Prompts[i].Timestamp > out.Prompts[j].Timestamp
	})
	if len(out.Prompts) > 40 {
		out.Prompts = out.Prompts[:40]
	}
	return out, sc.Err()
}

// extractContentText handles both string and array-of-content-block shapes.
func extractContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Try array of {"type": "text", "text": "..."} blocks.
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

// Total walks every project session file and returns the aggregated usage.
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
			total.CacheReadTokens += u.CacheReadTokens
			total.Messages += u.Messages
			total.Cost += u.Cost
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
	// Sort by modified desc.
	sortByModifiedDesc(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func slugToProject(slug string) string {
	// Claude Code slugs replace "/" with "-" and prepend "-".
	// We can't perfectly invert (because legitimate "-" can appear in paths),
	// but converting the leading "-" back to "/" is good enough for display.
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
