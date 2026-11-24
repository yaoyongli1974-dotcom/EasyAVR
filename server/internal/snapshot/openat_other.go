//go:build !linux

package snapshot

import "os"

// Security boundary (non-Linux, including Windows): os.Root pins the storage
// directory with an OS handle and rejects absolute paths, ".." escapes and
// symlinks whose target leaves the root; OpenSnapshot adds lexical traversal
// checks and a regular-file check on the returned handle. Unlike Linux
// openat2, this path may follow symlinks that remain inside the root and does
// not provide RESOLVE_NO_MAGICLINKS guarantees on every platform. It never
// falls back to an unconstrained os.Open of the catalog-relative name.
type snapshotRoot struct {
	root *os.Root
}

func newSnapshotRoot(dir string) (*snapshotRoot, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &snapshotRoot{root: root}, nil
}

func (r *snapshotRoot) close() error {
	if r.root == nil {
		return nil
	}
	err := r.root.Close()
	r.root = nil
	return err
}

func (r *snapshotRoot) open(rel string) (*os.File, error) {
	f, err := r.root.OpenFile(rel, os.O_RDONLY, 0)
	if err != nil {
		return nil, snapshotOpenError(err)
	}
	return f, nil
}
