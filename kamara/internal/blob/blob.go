// Package blob is Kamara's content-addressed blob store (ADR-0001 §10):
// a streaming interface so the backend is swappable (filesystem now, an
// S3-compatible impl behind the same interface at M6). Keys are content
// hashes, so Put is idempotent — writing an existing key is a no-op, which
// is how dedup is enforced at the storage layer.
package blob

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Store is the blob backend.
type Store interface {
	// Has reports whether a blob with this key exists.
	Has(ctx context.Context, key string) (bool, error)
	// Put stores r under key. Idempotent: if the key already exists it is a
	// no-op (content-addressed), so re-storing identical content is free.
	Put(ctx context.Context, key string, r io.Reader) error
	// Get opens the blob for streaming; the caller closes it.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes the blob (no error if absent).
	Delete(ctx context.Context, key string) error
}

// ErrNotFound is returned by Get for a missing key.
var ErrNotFound = os.ErrNotExist

// FS is a filesystem-backed Store rooted at a directory — a per-tenant
// PersistentVolume in the cluster.
type FS struct{ root string }

// NewFS returns a filesystem store rooted at dir (created if absent).
func NewFS(dir string) (*FS, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &FS{root: dir}, nil
}

// path shards by the first two bytes of the key to avoid huge directories.
// Keys are validated as hex content hashes (no separators) so they can't
// escape the root.
func (f *FS) path(key string) (string, error) {
	if len(key) < 4 || !isHex(key) {
		return "", fmt.Errorf("blob: invalid key %q", key)
	}
	return filepath.Join(f.root, key[0:2], key[2:4], key), nil
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (f *FS) Has(_ context.Context, key string) (bool, error) {
	p, err := f.path(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func (f *FS) Put(ctx context.Context, key string, r io.Reader) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if ok, err := f.Has(ctx, key); err != nil {
		return err
	} else if ok {
		return nil // content-addressed: already stored
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	// Write to a temp file, fsync, then atomically rename into place — a
	// crash never leaves a half-written blob at the content-addressed path.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, p)
}

func (f *FS) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := f.path(key)
	if err != nil {
		return nil, err
	}
	rc, err := os.Open(p)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return rc, err
}

func (f *FS) Delete(_ context.Context, key string) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

var _ Store = (*FS)(nil)
