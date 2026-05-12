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
	"strings"
	"time"
)

// Pricing in USD per million tokens. Approximate, updated for the Claude 4
// family. The Cost calculator uses these to convert tokens to dollars.
type Pricing struct {
	Input      float64
	Output     float64
	CacheWrite float64
	CacheRead  float64
}

// modelPricing returns the price table for a model, falling back to Opus 4
// for unknown models (overestimate is safer than zero).
func modelPricing(model string) Pricing {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "haiku"):
		return Pricing{Input: 0.80, Output: 4.00, CacheWrite: 1.00, CacheRead: 0.08}
	case strings.Contains(m, "sonnet"):
		return Pricing{Input: 3.00, Output: 15.00, CacheWrite: 3.75, CacheRead: 0.30}
	case strings.Contains(m, "opus"):
		return Pricing{Input: 15.00, Output: 75.00, CacheWrite: 18.75, CacheRead: 1.50}
	default:
		return Pricing{Input: 15.00, Output: 75.00, CacheWrite: 18.75, CacheRead: 1.50}
	}
}

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
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	Messages         int     `json:"messages"`
	Cost             float64 `json:"cost_usd"`
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
	Type    string `json:"type"`
	Message *struct {
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Timestamp string `json:"timestamp"`
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
		if ev.Type != "assistant" || ev.Message == nil || ev.Message.Usage == nil {
			continue
		}
		if !since.IsZero() && ev.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil && t.Before(since) {
				continue
			}
		}
		p := modelPricing(ev.Message.Model)
		usg := ev.Message.Usage
		u.InputTokens += usg.InputTokens
		u.OutputTokens += usg.OutputTokens
		u.CacheWriteTokens += usg.CacheCreationInputTokens
		u.CacheReadTokens += usg.CacheReadInputTokens
		u.Messages++
		u.Cost += (float64(usg.InputTokens)*p.Input +
			float64(usg.OutputTokens)*p.Output +
			float64(usg.CacheCreationInputTokens)*p.CacheWrite +
			float64(usg.CacheReadInputTokens)*p.CacheRead) / 1e6
	}
	return u, sc.Err()
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
