// Package local stores artifacts on the local disk, under $ROWBIRD_DATA_DIR/artifacts by default.
package local

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/storage"
)

// Storage keeps each object in a file, with its metadata in a ".meta" file beside it.
type Storage struct{ root string }

// New returns a storage rooted at dir, creating it.
func New(dir string) (*Storage, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Storage{root: dir}, nil
}

func (s *Storage) path(key string) (string, error) {
	if err := storage.CheckKey(key); err != nil {
		return "", err
	}
	return filepath.Join(s.root, filepath.FromSlash(key)), nil
}

// Put writes the object to a temporary file and renames it into place, so readers never see a
// partial file.
func (s *Storage) Put(ctx context.Context, key string, r io.Reader, meta plugin.ObjectMeta) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, ctxReader{ctx, r})
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	meta.Size = n
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p+".meta", b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Get opens an object.
func (s *Storage) Get(_ context.Context, key string) (io.ReadCloser, plugin.ObjectMeta, error) {
	var meta plugin.ObjectMeta
	p, err := s.path(key)
	if err != nil {
		return nil, meta, err
	}
	f, err := os.Open(p) //nolint:gosec // the key was checked and joined under the root
	if errors.Is(err, fs.ErrNotExist) {
		return nil, meta, plugin.ErrObjectNotFound
	}
	if err != nil {
		return nil, meta, err
	}
	if b, err := os.ReadFile(p + ".meta"); err == nil { //nolint:gosec // same path
		_ = json.Unmarshal(b, &meta)
	}
	if st, err := f.Stat(); err == nil {
		meta.Size = st.Size()
	}
	return f, meta, nil
}

// Delete removes an object; a missing object is not an error.
func (s *Storage) Delete(_ context.Context, key string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	for _, f := range []string{p, p + ".meta"} {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// PresignGet is not supported: downloads stream through Rowbird.
func (s *Storage) PresignGet(context.Context, string, time.Duration, plugin.ObjectMeta) (string, error) {
	return "", plugin.ErrPresignUnsupported
}

// ctxReader stops a copy when the context is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
