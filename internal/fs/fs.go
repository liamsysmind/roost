// Package fs implements the filesystem API used by the file-tree panel.
//
// All operations are constrained to a configurable root directory (default
// the user's home). Path traversal attempts are rejected before any I/O.
package fs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// API is the user-facing filesystem facade. Each method validates that the
// requested path stays within the configured Root.
type API struct {
	Root string // absolute path
}

// New returns an API rooted at the given path. The path is made absolute and
// symlinks resolved so subsequent containment checks are reliable.
func New(root string) (*API, error) {
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(expandHome(root))
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Allow non-existent paths — create on first use.
		if !os.IsNotExist(err) {
			return nil, err
		}
		resolved = abs
	}
	return &API{Root: resolved}, nil
}

// Entry is one directory listing item.
type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`     // relative to Root
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
	Mode    string    `json:"mode"`
}

// List returns the contents of a directory.
func (a *API) List(rel string) ([]Entry, error) {
	abs, err := a.resolve(rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", rel)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Entry{
			Name:    e.Name(),
			Path:    filepath.ToSlash(filepath.Join(rel, e.Name())),
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Mode:    info.Mode().String(),
		})
	}
	// Directories first, then by name.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Stat returns information about a single file or directory.
func (a *API) Stat(rel string) (*Entry, error) {
	abs, err := a.resolve(rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	return &Entry{
		Name:    info.Name(),
		Path:    rel,
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
		Mode:    info.Mode().String(),
	}, nil
}

// Open returns a ReadCloser for the file at rel. Caller must close.
func (a *API) Open(rel string) (io.ReadCloser, *Entry, error) {
	abs, err := a.resolve(rel)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, errors.New("path is a directory")
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, nil, err
	}
	return f, &Entry{
		Name:    info.Name(),
		Path:    rel,
		IsDir:   false,
		Size:    info.Size(),
		ModTime: info.ModTime(),
		Mode:    info.Mode().String(),
	}, nil
}

// Save writes a file at relDir/name from src. Existing files are overwritten.
func (a *API) Save(relDir, name string, src io.Reader) (*Entry, error) {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return nil, fmt.Errorf("invalid filename %q", name)
	}
	dirAbs, err := a.resolve(relDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dirAbs, 0o755); err != nil {
		return nil, err
	}
	dst := filepath.Join(dirAbs, name)
	if !a.within(dst) {
		return nil, errors.New("path escapes root")
	}
	tmp := dst + ".roost-upload"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(f, src); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return a.Stat(filepath.ToSlash(filepath.Join(relDir, name)))
}

// Mkdir creates a directory (including parents) at rel.
func (a *API) Mkdir(rel string) error {
	abs, err := a.resolve(rel)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0o755)
}

// Remove deletes a file or empty directory.
func (a *API) Remove(rel string, recursive bool) error {
	abs, err := a.resolve(rel)
	if err != nil {
		return err
	}
	if abs == a.Root {
		return errors.New("refusing to remove root")
	}
	if recursive {
		return os.RemoveAll(abs)
	}
	return os.Remove(abs)
}

// Rename moves a file/dir within the rooted tree.
func (a *API) Rename(from, to string) error {
	fromAbs, err := a.resolve(from)
	if err != nil {
		return err
	}
	toAbs, err := a.resolve(to)
	if err != nil {
		return err
	}
	return os.Rename(fromAbs, toAbs)
}

// resolve turns a Root-relative path into an absolute one and ensures the
// result is contained within Root.
func (a *API) resolve(rel string) (string, error) {
	clean := filepath.Clean("/" + strings.TrimPrefix(strings.TrimPrefix(rel, "/"), "./"))
	abs := filepath.Join(a.Root, clean)
	if !a.within(abs) {
		return "", fmt.Errorf("path escapes root: %q", rel)
	}
	return abs, nil
}

func (a *API) within(abs string) bool {
	rel, err := filepath.Rel(a.Root, abs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	return true
}

func expandHome(p string) string {
	if !strings.HasPrefix(p, "~/") && p != "~" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}
