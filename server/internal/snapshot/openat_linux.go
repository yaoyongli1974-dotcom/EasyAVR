//go:build linux

package snapshot

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Security boundary (Linux): openat2 resolves the catalog-relative name under
// a pinned root directory fd in a single syscall with RESOLVE_BENEATH (no
// escape above the root), RESOLVE_NO_SYMLINKS and RESOLVE_NO_MAGICLINKS (no
// symlink or /proc-style magic-link redirection). O_NOFOLLOW covers the final
// component and O_NONBLOCK keeps a FIFO/device from blocking the request;
// OpenSnapshot rejects anything that is not a regular file after the open.
// If the kernel (or a seccomp filter) does not support openat2, we degrade to
// os.Root rather than an unconstrained os.Open: see openFallback below.
type snapshotRoot struct {
	fd  int
	dir string
}

const snapshotOpenFlags = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK

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
	// about the requested file, so fall back to the still-confined os.Root
	// path. Every other errno describes the file itself (EXDEV escape, ELOOP
	// symlink, EPERM magic link, EACCES permission, ENOENT missing, ...).
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return r.openFallback(rel)
	}
	return nil, snapshotOpenError(err)
}

// openFallback is the safe degradation path for kernels without openat2.
// os.Root keeps the root pinned by fd and refuses absolute paths and any
// component (including a symlink target) that leaves the root. Unlike
// openat2 it may follow symlinks that stay inside the root, and it cannot
// reject every magic link on every filesystem; O_NOFOLLOW still rejects a
// final symlink and OpenSnapshot still enforces "regular file only".
func (r *snapshotRoot) openFallback(rel string) (*os.File, error) {
	root, err := os.OpenRoot(r.dir)
	if err != nil {
		return nil, ErrPathOutside
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(rel, snapshotOpenFlags, 0)
	if err != nil {
		return nil, snapshotOpenError(err)
	}
	return f, nil
}
