package ai

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type Handler struct {
	Reader *Reader
}

func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/usage", h.handleUsage)
	mux.HandleFunc("GET /api/ai/sessions", h.handleSessions)
}

func (h *Handler) handleUsage(w http.ResponseWriter, r *http.Request) {
	since := parseSince(r.URL.Query().Get("since"))
	u, err := h.Reader.Total(since)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"since": since,
		"usage": u,
	})
}

func (h *Handler) handleSessions(w http.ResponseWriter, r *http.Request) {
	since := parseSince(r.URL.Query().Get("since"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 25
	}
	out, err := h.Reader.Sessions(since, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, out)
}

// parseSince accepts:
//   - empty                → zero time (all-time)
//   - "today"              → start of today (local)
//   - "1h" / "30m" / "24h" → that duration ago
//   - RFC3339              → that exact time
func parseSince(s string) time.Time {
	switch s {
	case "":
		return time.Time{}
	case "today":
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
