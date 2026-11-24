//go:build linux

package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// Security boundary (Linux): openat2 resolves the catalog-relative name under
// a pinned root directory fd in a single syscall with RESOLVE_BENEATH (no
// escape above the root), RESOLVE_NO_SYMLINKS and RESOLVE_NO_MAGICLINKS (no
// symlink or /proc-style magic-link redirection). O_NOFOLLOW covers the final
// component and O_NONBLOCK keeps a FIFO/device from blocking the request;
// OpenSnapshot rejects anything that is not a regular file after the open.
// If the kernel (or a seccomp filter) does not support openat2, openFallback
// walks the name one component at a time with confined openat calls against
// that same pinned fd — it never reopens the root by its string path.
type snapshotRoot struct {
	fd  int
	dir string
}

const snapshotOpenFlags = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK

// Every intermediate component is opened as a directory and is the final
// component of its own openat call, so O_NOFOLLOW rejects a symlink there too.
const snapshotDirOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW

// forceFallbackForTest is a package-internal test hook: tests set it to drive
// the no-openat2 path even on kernels that support openat2. Production code
// never writes it. atomic keeps it safe when tests exercise concurrent opens.
var forceFallbackForTest atomic.Bool

func newSnapshotRoot(dir string) (*snapshotRoot, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return &snapshotRoot{fd: fd, dir: dir}, nil
}

func (r *snapshotRoot) close() error {
	if r.fd < 0 {
		return nil
	}
	err := unix.Close(r.fd)
	r.fd = -1
	return err
}

func (r *snapshotRoot) open(rel string) (*os.File, error) {
	if forceFallbackForTest.Load() {
		return r.openFallback(rel)
	}
	how := &unix.OpenHow{
		Flags:   uint64(snapshotOpenFlags),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	}
	fd, err := unix.Openat2(r.fd, rel, how)
	if err == nil {
		return os.NewFile(uintptr(fd), "snapshot"), nil
	}
	// ENOSYS: kernel older than 5.6. EINVAL: unsupported flags/resolve bits
	// (e.g. a seccomp filter rewriting the syscall). Neither says anything
	// about the requested file, so fall back to the still-confined openat
	// walk. Every other errno describes the file itself (EXDEV escape, ELOOP
	// symlink, EPERM magic link, EACCES permission, ENOENT missing, ...).
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return r.openFallback(rel)
	}
	return nil, snapshotOpenError(err)
}

// splitFallbackPath repeats the lexical checks of validSnapshotRelativePath
// for the fallback path and additionally rejects empty components (a//b),
// "." and a trailing slash. The fallback must not rely on the caller having
// checked first.
func splitFallbackPath(rel string) ([]string, bool) {
	if rel == "" || filepath.IsAbs(rel) || strings.HasSuffix(rel, "/") {
		return nil, false
	}
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, false
		}
	}
	return parts, true
}

// openFallback is the safe degradation path for kernels without openat2.
// It resolves each component with openat(2) relative to the root fd pinned by
// newSnapshotRoot, with O_NOFOLLOW on every component and O_DIRECTORY on every
// intermediate one. The root is not reopened by name: it may have been
// renamed or replaced since it was pinned. All intermediate descriptors are
// closed on return; the final file descriptor is handed to the caller open.
func (r *snapshotRoot) openFallback(rel string) (*os.File, error) {
	parts, ok := splitFallbackPath(rel)
	if !ok {
		return nil, ErrPathOutside
	}
	fd := r.fd
	var intermediate []int
	defer func() {
		for _, dirfd := range intermediate {
			_ = unix.Close(dirfd)
		}
	}()
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(fd, part, snapshotDirOpenFlags, 0)
		if err != nil {
			return nil, snapshotOpenError(err)
		}
		intermediate = append(intermediate, next)
		fd = next
	}
	filefd, err := unix.Openat(fd, parts[len(parts)-1], snapshotOpenFlags, 0)
	if err != nil {
		return nil, snapshotOpenError(err)
	}
	f := os.NewFile(uintptr(filefd), "snapshot")
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, snapshotOpenError(err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, ErrPathOutside
	}
	return f, nil
}
