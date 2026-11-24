package snapshot

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSnapshotAccessToken(t *testing.T) {
	const secret = "unit-test-secret"

	token, expires, err := SnapshotAccessToken(42, 7, time.Minute, secret)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !strings.HasPrefix(token, "s") || len(strings.Split(token, ".")) != 2 {
		t.Fatalf("unexpected token shape: %q", token)
	}
	id, user, parsedExpiry, err := ParseSnapshotAccessToken(token, secret)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if id != 42 || user != 7 {
		t.Fatalf("claims mismatch: id=%d user=%d", id, user)
	}
	if parsedExpiry.UnixMilli() != expires.UnixMilli() {
		t.Fatalf("expiry mismatch: %v vs %v", parsedExpiry, expires)
	}
	if d := time.Until(parsedExpiry); d < 59*time.Second || d > 61*time.Second {
		t.Fatalf("expiry not ~1m away: %v", d)
	}

	// Zero ttl uses the 5-minute default.
	_, defaultExpiry, err := SnapshotAccessToken(1, 2, 0, secret)
	if err != nil {
		t.Fatalf("issue default: %v", err)
	}
	if d := time.Until(defaultExpiry); d < DefaultTokenTTL-time.Second || d > DefaultTokenTTL+time.Second {
		t.Fatalf("default ttl not ~5m: %v", d)
	}

	// Changing the snapshot ID invalidates the signature.
	parts := strings.Split(strings.TrimPrefix(token, "s"), ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var p accessPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	p.ID = 43
	altered, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	tampered := "s" + base64.RawURLEncoding.EncodeToString(altered) + "." + parts[1]
	if _, _, _, err := ParseSnapshotAccessToken(tampered, secret); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tampered ID: got %v, want ErrInvalidToken", err)
	}

	// Expiry, wrong secret and malformed/prefix errors.
	expired, _, err := SnapshotAccessToken(42, 7, -time.Minute, secret)
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}
	if _, _, _, err := ParseSnapshotAccessToken(expired, secret); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("expired token: got %v, want ErrExpiredToken", err)
	}
	if _, _, _, err := ParseSnapshotAccessToken(token, "other-secret"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong secret: got %v, want ErrInvalidToken", err)
	}
	for _, bad := range []string{"x" + strings.TrimPrefix(token, "s"), "sabc", "s..", ""} {
		if _, _, _, err := ParseSnapshotAccessToken(bad, secret); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("malformed %q: got %v, want ErrInvalidToken", bad, err)
		}
	}
	if _, _, err := SnapshotAccessToken(1, 2, time.Minute, ""); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("empty secret issue: got %v", err)
	}
	if _, _, _, err := ParseSnapshotAccessToken(token, ""); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("empty secret parse: got %v", err)
	}
}

func TestResolvePath(t *testing.T) {
	dir := t.TempDir()
	s := NewService(nil, nil, dir)

	// ResolvePath is strict: the file must exist.
	sub := filepath.Join(dir, "12")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	valid := filepath.Join(sub, "34.jpg")
	if err := os.WriteFile(valid, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := s.ResolvePath(filepath.Join("12", "34.jpg"))
	if err != nil {
		t.Fatalf("valid path: %v", err)
	}
	want, err := filepath.EvalSymlinks(valid)
	if err != nil {
		t.Fatalf("eval expected: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// A missing file is an explicit, non-leaking error.
	for _, rel := range []string{filepath.Join("12", "missing.jpg"), "gone/x.jpg"} {
		_, err := s.ResolvePath(rel)
		if !errors.Is(err, ErrPathNotFound) || !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("missing %q: got %v, want ErrPathNotFound/fs.ErrNotExist", rel, err)
		}
		assertNoAbsPath(t, dir, err.Error())
	}

	base := filepath.Base(dir)
	for _, rel := range []string{
		"../outside.jpg",
		"12/../../../outside.jpg",
		"/etc/passwd",
		"",
		".",
		"../" + base + "-evil/x.jpg", // sibling with a matching prefix
	} {
		if got, err := s.ResolvePath(rel); err == nil {
			t.Fatalf("path %q should be rejected, resolved to %q", rel, got)
		} else if !errors.Is(err, ErrPathOutside) {
			t.Fatalf("path %q: got %v, want ErrPathOutside", rel, err)
		} else {
			assertNoAbsPath(t, dir, err.Error())
		}
	}
}

func assertNoAbsPath(t *testing.T, dir, msg string) {
	t.Helper()
	if strings.Contains(msg, dir) || strings.Contains(msg, filepath.Dir(dir)) {
		t.Fatalf("error leaks local path: %q", msg)
	}
}

// symlinkOrSkip keeps the symlink cases portable: Windows requires elevated
// privileges to create symlinks, in which case those cases are skipped rather
// than failing the whole suite.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestResolvePathRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	s := NewService(nil, nil, dir)
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.jpg")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}

	// A symlink inside the storage directory pointing at an outside file.
	symlinkOrSkip(t, outside, filepath.Join(dir, "escape.jpg"))
	if got, err := s.ResolvePath("escape.jpg"); !errors.Is(err, ErrPathOutside) {
		t.Fatalf("outside symlink: got %q/%v, want ErrPathOutside", got, err)
	} else {
		assertNoAbsPath(t, dir, err.Error())
	}

	// Multiple symlink levels must be rejected as well.
	symlinkOrSkip(t, outside, filepath.Join(dir, "level2.jpg"))
	symlinkOrSkip(t, filepath.Join(dir, "level2.jpg"), filepath.Join(dir, "level1.jpg"))
	if got, err := s.ResolvePath("level1.jpg"); !errors.Is(err, ErrPathOutside) {
		t.Fatalf("multi-level symlink: got %q/%v, want ErrPathOutside", got, err)
	}

	// A symlinked subdirectory pointing outside, with a file under it.
	symlinkOrSkip(t, outsideDir, filepath.Join(dir, "outdir"))
	if got, err := s.ResolvePath(filepath.Join("outdir", "secret.jpg")); !errors.Is(err, ErrPathOutside) {
		t.Fatalf("symlinked parent: got %q/%v, want ErrPathOutside", got, err)
	}

	// A dangling symlink pointing outside is rejected rather than reported
	// as a plain missing file.
	symlinkOrSkip(t, filepath.Join(outsideDir, "nope.jpg"), filepath.Join(dir, "dangling.jpg"))
	if _, err := s.ResolvePath("dangling.jpg"); !errors.Is(err, ErrPathOutside) {
		t.Fatalf("dangling symlink: got %v, want ErrPathOutside", err)
	}
}

func TestResolvePathAllowsInternalSymlink(t *testing.T) {
	dir := t.TempDir()
	s := NewService(nil, nil, dir)
	sub := filepath.Join(dir, "12")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	real := filepath.Join(sub, "34.jpg")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	symlinkOrSkip(t, real, filepath.Join(dir, "alias.jpg"))
	got, err := s.ResolvePath("alias.jpg")
	if err != nil {
		t.Fatalf("internal symlink: %v", err)
	}
	want, _ := filepath.EvalSymlinks(real)
	if got != want {
		t.Fatalf("internal symlink resolved to %q, want %q", got, want)
	}
}

func TestResolvePathConcurrentDeleteAndAccess(t *testing.T) {
	dir := t.TempDir()
	s := NewService(nil, nil, dir)
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval root: %v", err)
	}

	outside := filepath.Join(t.TempDir(), "secret.jpg")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	normal := filepath.Join(dir, "1.jpg")
	if err := os.WriteFile(normal, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write normal: %v", err)
	}
	symlinkOrSkip(t, outside, filepath.Join(dir, "escape.jpg"))

	var normalOK, escapeOK, leaked atomic.Int64
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			_ = os.Remove(normal)
			_ = os.WriteFile(normal, []byte("ok"), 0o644)
		}
		close(stop)
	}()

	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if p, err := s.ResolvePath("1.jpg"); err == nil {
					normalOK.Add(1)
					if !strings.HasPrefix(p, root+string(os.PathSeparator)) {
						leaked.Add(1)
					}
				}
				if p, err := s.ResolvePath("escape.jpg"); err == nil {
					escapeOK.Add(1)
					if !strings.HasPrefix(p, root+string(os.PathSeparator)) {
						leaked.Add(1)
					}
				}
			}
		}()
	}
	wg.Wait()

	if escapeOK.Load() != 0 {
		t.Fatalf("symlink escape resolved %d times under concurrent access", escapeOK.Load())
	}
	if normalOK.Load() == 0 {
		t.Fatal("normal path never resolved while being deleted/recreated")
	}
	if leaked.Load() != 0 {
		t.Fatalf("resolved path escaped the root %d times", leaked.Load())
	}
}

// readOpenSnapshotHandle reads through the handle returned by OpenSnapshot.
// It is goroutine-safe (unlike t.Fatal helpers used by the callers).
func readOpenSnapshotHandle(s *Service, rel string) (string, error) {
	f, err := s.OpenSnapshot(rel)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func raceMarkers() (inner, outside string) {
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	return "INNER-" + stamp, "OUTSIDE-" + stamp
}

func assertOpenSnapshotOutside(t *testing.T, s *Service, dir, rel string) {
	t.Helper()
	f, err := s.OpenSnapshot(rel)
	if err == nil {
		_ = f.Close()
		t.Fatalf("OpenSnapshot(%q) unexpectedly succeeded", rel)
	}
	if !errors.Is(err, ErrPathOutside) {
		t.Fatalf("OpenSnapshot(%q): got %v, want ErrPathOutside", rel, err)
	}
	assertNoAbsPath(t, dir, err.Error())
}

func TestOpenSnapshotRejectsParentSymlink(t *testing.T) {
	dir := t.TempDir()
	s := NewService(nil, nil, dir)
	outsideDir := t.TempDir()
	_, outside := raceMarkers()
	if err := os.WriteFile(filepath.Join(outsideDir, "1.jpg"), []byte(outside), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "12"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "12")); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	symlinkOrSkip(t, outsideDir, filepath.Join(dir, "12"))
	assertOpenSnapshotOutside(t, s, dir, filepath.Join("12", "1.jpg"))
}

func TestOpenSnapshotRejectsFinalSymlink(t *testing.T) {
	dir := t.TempDir()
	s := NewService(nil, nil, dir)
	outside := filepath.Join(t.TempDir(), "secret.jpg")
	if err := os.WriteFile(outside, []byte("TOP-SECRET"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	symlinkOrSkip(t, outside, filepath.Join(dir, "link.jpg"))
	assertOpenSnapshotOutside(t, s, dir, "link.jpg")
}

func TestOpenSnapshotRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	s := NewService(nil, nil, dir)
	base := filepath.Base(dir)
	for _, rel := range []string{
		"",
		".",
		"..",
		"../outside.jpg",
		"12/../../../outside.jpg",
		"../" + base + "-evil/secret.jpg",
		filepath.Join(dir, "1.jpg"),
	} {
		assertOpenSnapshotOutside(t, s, dir, rel)
	}
}

func TestOpenSnapshotRejectsMagicLink(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("magic-link handling is Linux-specific")
	}
	dir := t.TempDir()
	s := NewService(nil, nil, dir)
	outside := filepath.Join(t.TempDir(), "secret.jpg")
	if err := os.WriteFile(outside, []byte("MAGIC-SECRET"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	f, err := os.Open(outside)
	if err != nil {
		t.Fatalf("open outside fd: %v", err)
	}
	defer func() { _ = f.Close() }()
	magic := "/proc/self/fd/" + strconv.Itoa(int(f.Fd()))
	if info, err := os.Lstat(magic); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Skipf("magic link unavailable at %s: %v", magic, err)
	}
	symlinkOrSkip(t, magic, filepath.Join(dir, "magic.jpg"))
	assertOpenSnapshotOutside(t, s, dir, "magic.jpg")
}

// TestOpenSnapshotParentSwapRace repeatedly replaces a parent directory with
// a symlink to a directory holding a uniquely marked file outside the root.
// Readers that get a handle must never observe the outside marker.
func TestOpenSnapshotParentSwapRace(t *testing.T) {
	// The writer needs symlinks throughout the race; skip where unavailable.
	symlinkOrSkip(t, "target", filepath.Join(t.TempDir(), "probe-link"))
	dir := t.TempDir()
	base := t.TempDir()
	s := NewService(nil, nil, dir)
	inner, outside := raceMarkers()
	safeDir := filepath.Join(base, "safe")
	evilDir := filepath.Join(base, "evil")
	link := filepath.Join(dir, "12")
	for _, d := range []string{safeDir, evilDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(safeDir, "1.jpg"), []byte(inner), 0o644); err != nil {
		t.Fatalf("write inner: %v", err)
	}
	if err := os.WriteFile(filepath.Join(evilDir, "1.jpg"), []byte(outside), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	recreateSafe := func() error {
		if err := os.Mkdir(safeDir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(safeDir, "1.jpg"), []byte(inner), 0o644)
	}
	// Precondition: a real parent directory serves the inner file.
	if err := os.Rename(safeDir, link); err != nil {
		t.Fatalf("install safe dir: %v", err)
	}
	if got, err := readOpenSnapshotHandle(s, filepath.Join("12", "1.jpg")); err != nil || got != inner {
		t.Fatalf("precondition: got %q/%v, want %q", got, err, inner)
	}

	const iterations = 1500
	var leaked, opened, writerErr atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := readOpenSnapshotHandle(s, filepath.Join("12", "1.jpg"))
				if err == nil {
					opened.Add(1)
					if strings.Contains(got, outside) {
						leaked.Add(1)
					}
				}
			}
		}()
	}
	for i := 0; i < iterations; i++ {
		if err := os.RemoveAll(link); err != nil {
			writerErr.Add(1)
			break
		}
		if err := os.Symlink(evilDir, link); err != nil {
			writerErr.Add(1)
			break
		}
		if err := os.Remove(link); err != nil {
			writerErr.Add(1)
			break
		}
		if err := recreateSafe(); err != nil {
			writerErr.Add(1)
			break
		}
		if err := os.Rename(safeDir, link); err != nil {
			writerErr.Add(1)
			break
		}
	}
	close(stop)
	wg.Wait()
	if writerErr.Load() != 0 {
		t.Fatalf("parent swap setup failed %d times", writerErr.Load())
	}
	if opened.Load() == 0 {
		t.Fatal("no snapshot was opened during the parent swap race")
	}
	if leaked.Load() != 0 {
		t.Fatalf("read outside-root content %d times during parent swap race", leaked.Load())
	}
}

// TestOpenSnapshotFileReplacementRace swaps the final component between a
// regular file and a symlink to a uniquely marked outside file while readers
// open it. Any successful read must come from an already-opened inner inode.
func TestOpenSnapshotFileReplacementRace(t *testing.T) {
	// The writer needs symlinks throughout the race; skip where unavailable.
	symlinkOrSkip(t, "target", filepath.Join(t.TempDir(), "probe-link"))
	dir := t.TempDir()
	s := NewService(nil, nil, dir)
	inner, outside := raceMarkers()
	outsideFile := filepath.Join(t.TempDir(), "secret.jpg")
	if err := os.WriteFile(outsideFile, []byte(outside), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	path := filepath.Join(dir, "1.jpg")
	if err := os.WriteFile(path, []byte(inner), 0o644); err != nil {
		t.Fatalf("write inner: %v", err)
	}
	if got, err := readOpenSnapshotHandle(s, "1.jpg"); err != nil || got != inner {
		t.Fatalf("precondition: got %q/%v, want %q", got, err, inner)
	}

	const iterations = 1500
	var leaked, opened, writerErr atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := readOpenSnapshotHandle(s, "1.jpg")
				if err == nil {
					opened.Add(1)
					if strings.Contains(got, outside) {
						leaked.Add(1)
					}
				}
			}
		}()
	}
	tmp := filepath.Join(dir, ".inner.tmp")
	for i := 0; i < iterations; i++ {
		_ = os.Remove(path)
		var err error
		if i%2 == 0 {
			// Publish the regular file atomically so a reader never sees a
			// half-written file; the only interesting race is the swap itself.
			if err = os.WriteFile(tmp, []byte(inner), 0o644); err == nil {
				err = os.Rename(tmp, path)
			}
		} else {
			err = os.Symlink(outsideFile, path)
		}
		if err != nil {
			writerErr.Add(1)
			break
		}
	}
	close(stop)
	wg.Wait()
	if writerErr.Load() != 0 {
		t.Fatalf("file replacement setup failed %d times", writerErr.Load())
	}
	if opened.Load() == 0 {
		t.Fatal("no snapshot was opened during the file replacement race")
	}
	if leaked.Load() != 0 {
		t.Fatalf("read outside-root content %d times during file replacement race", leaked.Load())
	}
}

func TestOpenSnapshotRegularFileOnly(t *testing.T) {
	dir := t.TempDir()
	s := NewService(nil, nil, dir)
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	assertOpenSnapshotOutside(t, s, dir, "folder")

	// A FIFO is a non-regular file. mkfifo is only available on Unix; a
	// missing utility is a skip, not a false pass.
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("mkfifo"); err == nil {
			fifo := filepath.Join(dir, "pipe")
			out, err := exec.Command("mkfifo", fifo).CombinedOutput()
			if err != nil {
				t.Skipf("mkfifo failed: %v (%s)", err, strings.TrimSpace(string(out)))
			}
			assertOpenSnapshotOutside(t, s, dir, "pipe")
		} else {
			t.Skip("mkfifo unavailable: FIFO case skipped")
		}
	}

	// A normal nested relative path still works.
	if err := os.MkdirAll(filepath.Join(dir, "12"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "12", "34.jpg"), []byte("JPEG"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, err := readOpenSnapshotHandle(s, filepath.Join("12", "34.jpg")); err != nil || got != "JPEG" {
		t.Fatalf("normal path: got %q/%v, want JPEG", got, err)
	}
	if _, err := s.OpenSnapshot("12/gone.jpg"); !errors.Is(err, ErrPathNotFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: got %v, want ErrPathNotFound/fs.ErrNotExist", err)
	}
}
