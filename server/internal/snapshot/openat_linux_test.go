//go:build linux

package snapshot

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/unix"
)

func newFallbackTestRoot(t *testing.T, dir string) *snapshotRoot {
	t.Helper()
	r, err := newSnapshotRoot(dir)
	if err != nil {
		t.Fatalf("newSnapshotRoot(%q): %v", dir, err)
	}
	t.Cleanup(func() { _ = r.close() })
	return r
}

func writeFallbackTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func readAndCloseFallbackTestFile(t *testing.T, f *os.File) string {
	t.Helper()
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(data)
}

func assertFallbackRejected(t *testing.T, r *snapshotRoot, rel string) {
	t.Helper()
	f, err := r.openFallback(rel)
	if err == nil {
		_ = f.Close()
		t.Errorf("openFallback(%q) unexpectedly succeeded", rel)
		return
	}
	if !errors.Is(err, ErrPathOutside) {
		t.Errorf("openFallback(%q): got %v, want ErrPathOutside", rel, err)
	}
}

func forceFallbackForTestOn(t *testing.T) {
	t.Helper()
	forceFallbackForTest.Store(true)
	t.Cleanup(func() { forceFallbackForTest.Store(false) })
}

func TestOpenFallbackRegularFile(t *testing.T) {
	dir := t.TempDir()
	writeFallbackTestFile(t, filepath.Join(dir, "a", "b", "data.txt"), "inside")
	r := newFallbackTestRoot(t, dir)

	f, err := r.openFallback("a/b/data.txt")
	if err != nil {
		t.Fatalf("openFallback: %v", err)
	}
	if got := readAndCloseFallbackTestFile(t, f); got != "inside" {
		t.Fatalf("content = %q, want %q", got, "inside")
	}
}

func TestOpenFallbackRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeFallbackTestFile(t, filepath.Join(outside, "secret.txt"), "OUTSIDE")
	writeFallbackTestFile(t, filepath.Join(dir, "real", "data.txt"), "inside")

	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatalf("symlink parent: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "finalLink")); err != nil {
		t.Fatalf("symlink final absolute: %v", err)
	}
	if err := os.Symlink("real/data.txt", filepath.Join(dir, "relativeLink")); err != nil {
		t.Fatalf("symlink final relative: %v", err)
	}

	r := newFallbackTestRoot(t, dir)
	for _, rel := range []string{"escape/secret.txt", "finalLink", "relativeLink", "real/../finalLink"} {
		assertFallbackRejected(t, r, rel)
	}
}

func TestOpenFallbackRejectsEscapesAndMalformedPaths(t *testing.T) {
	dir := t.TempDir()
	writeFallbackTestFile(t, filepath.Join(dir, "a", "data.txt"), "inside")
	r := newFallbackTestRoot(t, dir)

	for _, rel := range []string{
		"",
		".",
		"..",
		"../outside.txt",
		"a/../data.txt",
		"a/./data.txt",
		"a//data.txt",
		"a/data.txt/",
		"/etc/passwd",
	} {
		assertFallbackRejected(t, r, rel)
	}
}

func TestOpenFallbackRejectsNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := unix.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	r := newFallbackTestRoot(t, dir)

	// A directory or FIFO may open successfully, but the regular-file check
	// (the same f.Stat check OpenSnapshot applies) must reject the handle.
	for _, rel := range []string{"subdir", "pipe"} {
		f, err := r.openFallback(rel)
		if err == nil {
			_ = f.Close()
			t.Errorf("openFallback(%q) returned a handle for a non-regular file", rel)
			continue
		}
		if !errors.Is(err, ErrPathOutside) {
			t.Errorf("openFallback(%q): got %v, want ErrPathOutside", rel, err)
		}
	}
}

func TestOpenFallbackErrorMapping(t *testing.T) {
	dir := t.TempDir()
	r := newFallbackTestRoot(t, dir)

	for _, rel := range []string{"missing.txt", "missing/deep.txt"} {
		if _, err := r.openFallback(rel); !errors.Is(err, ErrPathNotFound) {
			t.Errorf("openFallback(%q): got %v, want ErrPathNotFound", rel, err)
		}
	}
}

func TestOpenatUnavailableUsesConfinedFallback(t *testing.T) {
	forceFallbackForTestOn(t)

	dir := t.TempDir()
	outside := t.TempDir()
	writeFallbackTestFile(t, filepath.Join(dir, "a", "data.txt"), "inside")
	writeFallbackTestFile(t, filepath.Join(outside, "secret.txt"), "OUTSIDE")
	if err := os.Symlink(outside, filepath.Join(dir, "a", "escape")); err != nil {
		t.Fatalf("symlink parent: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "finalLink")); err != nil {
		t.Fatalf("symlink final: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	s := NewService(nil, nil, dir)
	f, err := s.OpenSnapshot("a/data.txt")
	if err != nil {
		t.Fatalf("OpenSnapshot via fallback: %v", err)
	}
	if got := readAndCloseFallbackTestFile(t, f); got != "inside" {
		t.Fatalf("content = %q, want %q", got, "inside")
	}
	for _, rel := range []string{"a/escape/secret.txt", "finalLink", "adir", ".."} {
		g, err := s.OpenSnapshot(rel)
		if err == nil {
			_ = g.Close()
			t.Errorf("OpenSnapshot(%q) unexpectedly succeeded via fallback", rel)
			continue
		}
		if !errors.Is(err, ErrPathOutside) {
			t.Errorf("OpenSnapshot(%q): got %v, want ErrPathOutside", rel, err)
		}
	}
	if _, err := s.OpenSnapshot("missing.txt"); !errors.Is(err, ErrPathNotFound) {
		t.Errorf("OpenSnapshot(missing): got %v, want ErrPathNotFound", err)
	}
}

func TestOpenFallbackUsesPinnedRootAfterRename(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	renamed := filepath.Join(base, "root-renamed")
	writeFallbackTestFile(t, filepath.Join(root, "data.txt"), "inside")
	r := newFallbackTestRoot(t, root)

	if err := os.Rename(root, renamed); err != nil {
		t.Fatalf("rename root: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old root path still resolves: %v", err)
	}

	f, err := r.openFallback("data.txt")
	if err != nil {
		t.Fatalf("openFallback through renamed root: %v", err)
	}
	if got := readAndCloseFallbackTestFile(t, f); got != "inside" {
		t.Fatalf("content = %q, want %q", got, "inside")
	}
}

func TestOpenFallbackRootReplaceRace(t *testing.T) {
	const (
		readRounds   = 1000
		renameRounds = readRounds
		inside       = "INSIDE-SENTINEL"
		outside      = "OUTSIDE-SENTINEL"
	)
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outsideDir := filepath.Join(base, "outside")
	swap := filepath.Join(base, "swap")
	writeFallbackTestFile(t, filepath.Join(root, "data.txt"), inside)
	writeFallbackTestFile(t, filepath.Join(outsideDir, "data.txt"), outside)
	r := newFallbackTestRoot(t, root)

	var renames atomic.Int64
	var renameErr atomic.Value
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < renameRounds; i++ {
			steps := [][2]string{
				{root, swap},
				{outsideDir, root},
				{root, outsideDir},
				{swap, root},
			}
			for _, step := range steps {
				if err := os.Rename(step[0], step[1]); err != nil {
					renameErr.Store(err)
					return
				}
			}
			renames.Add(1)
		}
	}()
	// Also stop the renamer before t.TempDir cleanup if an assertion aborts.
	defer func() { <-done }()

	reads := 0
readLoop:
	for {
		f, err := r.openFallback("data.txt")
		if err != nil {
			t.Fatalf("round %d: openFallback: %v", reads, err)
		}
		data, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			t.Fatalf("round %d: read: %v", reads, err)
		}
		if string(data) != inside {
			t.Fatalf("round %d: content = %q, want %q (root path was replaced)", reads, data, inside)
		}
		reads++
		if reads >= readRounds {
			select {
			case <-done:
				// At least 1000 reads overlapped at least 1000 renames.
				break readLoop
			default:
				continue
			}
		}
	}
	if err, ok := renameErr.Load().(error); ok {
		t.Fatalf("rename goroutine: %v", err)
	}
	if got := renames.Load(); got < readRounds {
		t.Fatalf("only %d rename rounds completed, want >= %d", got, readRounds)
	}
}
