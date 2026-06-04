package fs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

// Handler exposes the API over HTTP.
type Handler struct {
	API *API
}

// Mount registers /api/fs/* routes on the given mux.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/fs/list", h.handleList)
	mux.HandleFunc("GET /api/fs/download", h.handleDownload)
	mux.HandleFunc("GET /api/fs/preview", h.handlePreview)
	mux.HandleFunc("POST /api/fs/upload", h.handleUpload)
	mux.HandleFunc("POST /api/fs/mkdir", h.handleMkdir)
	mux.HandleFunc("POST /api/fs/rename", h.handleRename)
	mux.HandleFunc("DELETE /api/fs/remove", h.handleRemove)
	mux.HandleFunc("GET /api/fs/root", h.handleRoot)
	mux.HandleFunc("POST /api/fs/exist", h.handleExist)
}

func (h *Handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"root": h.API.Root})
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	entries, err := h.API.List(rel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    normalizeRel(rel),
		"entries": entries,
	})
}

func (h *Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	rc, entry, err := h.API.Open(rel)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", entry.Size))
	disposition := fmt.Sprintf(`attachment; filename*=UTF-8''%s`, url.PathEscape(filepath.Base(entry.Name)))
	w.Header().Set("Content-Disposition", disposition)
	_, _ = io.Copy(w, rc)
}

// handlePreview serves the file inline (no Content-Disposition attachment).
// Content-Type is chosen by extension first, then sniffed from the body
// (http.DetectContentType) so extensionless binaries don't masquerade as
// text. Response is capped so the browser can't choke on huge files.
const previewMaxBytes = 8 << 20 // 8 MB

func (h *Handler) handlePreview(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	rc, entry, err := h.API.Open(rel)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	defer rc.Close()
	if entry.Size > previewMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Errorf("file is %d bytes; preview cap is %d. Use download instead.", entry.Size, previewMaxBytes))
		return
	}

	body, err := io.ReadAll(rc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ct := previewMime(entry.Name)
	if ct == "" {
		ct = http.DetectContentType(body)
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.Header().Set("X-Roost-Filename", entry.Name)

	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

// previewMime maps a filename to a Content-Type string suitable for inline
// rendering. Anything unknown falls through to application/octet-stream so
// the browser doesn't guess wrong on binaries.
func previewMime(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".txt", ".md", ".rst", ".log",
		".json", ".yaml", ".yml", ".toml", ".xml", ".csv", ".tsv",
		".html", ".htm", ".css",
		".js", ".ts", ".tsx", ".jsx", ".mjs",
		".go", ".py", ".rb", ".rs", ".c", ".h", ".cpp", ".hpp", ".cc",
		".java", ".kt", ".swift", ".m", ".mm",
		".sh", ".bash", ".zsh", ".fish",
		".sql", ".diff", ".patch", ".conf", ".cfg", ".ini", ".env":
		return "text/plain; charset=utf-8"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	case ".pdf":
		return "application/pdf"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	case ".m4a":
		return "audio/mp4"
	}
	// Unknown extension — let handlePreview sniff body bytes for a
	// content-type. Returning "" rather than guessing means binaries
	// without an extension don't get served as text/plain.
	return ""
}

// handleUpload accepts multipart/form-data with one "path" field and one or
// more "file" fields. We parse the request *streaming* (mime/multipart
// directly) rather than via r.ParseMultipartForm so large files never get
// buffered through /tmp — each part is piped straight into its destination.
// Cap is loose (100 GB per request) because the workload is single-user
// self-hosted artifact moves; the OS still enforces disk-full.
const uploadMaxBytes = 100 << 30 // 100 GB

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, uploadMaxBytes)

	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		writeError(w, http.StatusBadRequest, errors.New("expected multipart/form-data"))
		return
	}
	boundary := params["boundary"]
	if boundary == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing multipart boundary"))
		return
	}

	mr := multipart.NewReader(r.Body, boundary)
	dir := ""
	saved := []*Entry{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		name := part.FormName()
		filename := part.FileName()

		// "path" field — destination directory, relative to fs root.
		// Read up to 8 KB (path strings are tiny in practice).
		if name == "path" && filename == "" {
			b, err := io.ReadAll(io.LimitReader(part, 8<<10))
			_ = part.Close()
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			dir = string(b)
			continue
		}

		// "file" field — stream straight to disk through h.API.Save,
		// which writes to a temp sibling then renames atomically.
		if name == "file" {
			if filename == "" {
				_ = part.Close()
				continue
			}
			entry, err := h.API.Save(dir, filepath.Base(filename), part)
			_ = part.Close()
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			saved = append(saved, entry)
			continue
		}

		// Unknown field — drain it so we can continue.
		_, _ = io.Copy(io.Discard, part)
		_ = part.Close()
	}

	if len(saved) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no file uploaded"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": saved})
}

func (h *Handler) handleMkdir(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rel := r.FormValue("path")
	if err := h.API.Mkdir(rel); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleRename(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	from := r.FormValue("from")
	to := r.FormValue("to")
	if err := h.API.Rename(from, to); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleRemove(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	recursive := r.URL.Query().Get("recursive") == "1"
	if err := h.API.Remove(rel, recursive); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleExist takes { cwd: string, paths: [string] } and returns
// { results: [{ input, rel, exists }] }. Used by the terminal-link provider
// to validate path-shaped tokens before decorating them as clickable, so an
// agent quoting a hypothetical path doesn't render as a dead link.
func (h *Handler) handleExist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cwd   string   `json:"cwd"`
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	const maxPaths = 256
	if len(req.Paths) > maxPaths {
		writeError(w, http.StatusBadRequest, fmt.Errorf("too many paths: %d (max %d)", len(req.Paths), maxPaths))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results": h.API.BatchExist(req.Cwd, req.Paths),
	})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func normalizeRel(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}
