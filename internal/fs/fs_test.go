package fs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newTestRoot returns a *API rooted in a fresh temp dir, plus the dir's
// real path (after EvalSymlinks — macOS's /tmp is itself a symlink).
func newTestRoot(t *testing.T) (*API, string) {
	t.Helper()
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval temp dir: %v", err)
	}
	api, err := New(real)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return api, real
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestResolveRejectsTraversal(t *testing.T) {
	api, _ := newTestRoot(t)
	cases := []string{
		"../etc/passwd",
		"../../etc/passwd",
		"foo/../../etc/passwd",
		"/etc/passwd",
	}
	for _, in := range cases {
		got, err := api.resolve(in)
		if err != nil {
			// Either rejected outright, or normalised into root — both fine
			// because the lexical traversal was neutralised.
			continue
		}
		rel, relErr := filepath.Rel(api.Root, got)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("resolve(%q) = %q, escapes root", in, got)
		}
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	api, root := newTestRoot(t)

	// Create an outside-of-root target with a sentinel file.
	outside := t.TempDir()
	outside, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatalf("eval outside: %v", err)
	}
	writeFile(t, filepath.Join(outside, "secret"), "leak")

	// Plant a symlink inside root pointing at the outside directory.
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Reads through the symlink — at the leaf and through a component —
	// must be rejected, not chased to the outside target.
	if _, _, err := api.Open("link/secret"); err == nil {
		t.Errorf("Open through symlink should fail")
	}
	if _, err := api.List("link"); err == nil {
		t.Errorf("List through symlink should fail")
	}
	if _, err := api.Stat("link/secret"); err == nil {
		t.Errorf("Stat through symlink should fail")
	}

	// Writes through the symlink must also be rejected — otherwise we'd
	// create files at outside/uploaded.
	if _, err := api.Save("link", "uploaded", strings.NewReader("x")); err == nil {
		t.Errorf("Save through symlink should fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "uploaded")); err == nil {
		t.Errorf("Save leaked file to outside-root target")
	}
	if err := api.Mkdir("link/newdir"); err == nil {
		t.Errorf("Mkdir through symlink should fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "newdir")); err == nil {
		t.Errorf("Mkdir leaked directory to outside-root target")
	}
}

func TestRemoveSymlinkLeavesTargetIntact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	api, root := newTestRoot(t)

	outside := t.TempDir()
	target := filepath.Join(outside, "keep")
	writeFile(t, target, "preserve me")

	linkPath := filepath.Join(root, "link")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := api.Remove("link", false); err != nil {
		t.Fatalf("Remove(symlink): %v", err)
	}
	if _, err := os.Lstat(linkPath); !os.IsNotExist(err) {
		t.Errorf("symlink should be gone, got err=%v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("target should still exist, got err=%v", err)
	}
}

func TestRenameSymlinkLeavesTargetIntact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	api, root := newTestRoot(t)

	outside := t.TempDir()
	target := filepath.Join(outside, "keep")
	writeFile(t, target, "stay")

	linkPath := filepath.Join(root, "link")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := api.Rename("link", "moved"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	movedPath := filepath.Join(root, "moved")
	info, err := os.Lstat(movedPath)
	if err != nil {
		t.Fatalf("moved symlink missing: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("moved entry is not a symlink (mode=%v)", info.Mode())
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("target should still exist, got err=%v", err)
	}
}

func TestInRootSymlinkAllowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	api, root := newTestRoot(t)

	// Real file + symlink inside root pointing to it. Reading via the
	// symlink should succeed: containment is the invariant, not symlink
	// presence.
	if err := os.Mkdir(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(root, "data", "hello"), "hi")
	if err := os.Symlink(filepath.Join(root, "data"), filepath.Join(root, "alias")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	entries, err := api.List("alias")
	if err != nil {
		t.Fatalf("List through in-root symlink: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "hello" {
		t.Errorf("unexpected entries: %+v", entries)
	}

	rc, _, err := api.Open("alias/hello")
	if err != nil {
		t.Fatalf("Open through in-root symlink: %v", err)
	}
	rc.Close()
}

// Save() onto a leaf that is itself an in-root symlink should write through
// the symlink — updating the target's contents — rather than replacing the
// symlink inode with a regular file. The latter silently breaks any workflow
// that relies on symlink aliases (e.g. uploading to a project's "current.log"
// alias). resolveLeaf for the leaf produces that broken behavior; Save must
// follow the symlink to its target like a normal write would.
func TestSaveThroughInRootSymlinkPreservesLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	api, root := newTestRoot(t)

	realPath := filepath.Join(root, "real.txt")
	writeFile(t, realPath, "original")

	aliasPath := filepath.Join(root, "alias.txt")
	if err := os.Symlink(realPath, aliasPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := api.Save("", "alias.txt", strings.NewReader("updated")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// alias.txt must still be a symlink, not a regular file.
	info, err := os.Lstat(aliasPath)
	if err != nil {
		t.Fatalf("lstat alias: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("alias.txt became a regular file (mode=%v); the symlink was replaced", info.Mode())
	}

	// And the real target should hold the new content.
	got, err := os.ReadFile(realPath)
	if err != nil {
		t.Fatalf("read real: %v", err)
	}
	if string(got) != "updated" {
		t.Errorf("real.txt = %q, want %q", got, "updated")
	}
}

func TestSaveAndMkdirNormalCases(t *testing.T) {
	api, root := newTestRoot(t)

	if _, err := api.Save("uploads", "file.txt", strings.NewReader("body")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "uploads", "file.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "body" {
		t.Errorf("body = %q, want %q", got, "body")
	}

	if err := api.Mkdir("nested/deep/dir"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "nested", "deep", "dir"))
	if err != nil || !info.IsDir() {
		t.Errorf("mkdir target missing or not a dir: %v", err)
	}
}

func TestSaveRejectsBadFilename(t *testing.T) {
	api, _ := newTestRoot(t)
	for _, name := range []string{"", "../escape", "sub/dir"} {
		if _, err := api.Save("", name, strings.NewReader("x")); err == nil {
			t.Errorf("Save(name=%q) should fail", name)
		}
	}
}
