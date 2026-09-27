// Package backup writes and restores backups of a SQLite store (docs/spec/08-operations.md,
// "Backup & restore"). A backup is a gzip-compressed tar archive holding a manifest, a consistent
// copy of the database taken online with VACUUM INTO and, optionally, the local artifacts. The
// master key is never part of it: only its id is recorded, so a restore can tell whether the key
// in use can read the secrets. Postgres stores are backed up with pg_dump instead.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/rowbird/rowbird/internal/store"
)

// Names inside the archive.
const (
	ManifestName    = "manifest.json"
	DatabaseName    = "rowbird.db"
	artifactsPrefix = "artifacts/"
)

// FormatVersion is the archive layout written by this version.
const FormatVersion = 1

// Manifest describes a backup.
type Manifest struct {
	Format        int       `json:"format"`
	Version       string    `json:"rowbird_version"`
	Schema        int64     `json:"schema_version"`
	KeyID         string    `json:"key_id"`
	CreatedAt     time.Time `json:"created_at"`
	WithArtifacts bool      `json:"with_artifacts"`
}

// Options control Write.
type Options struct {
	// ArtifactsDir, when set, adds the local artifacts under artifacts/.
	ArtifactsDir string
	// KeyID and Version are recorded in the manifest.
	KeyID, Version string
	Now            func() time.Time
}

// Write streams a backup of st to w.
func Write(ctx context.Context, st *store.Store, w io.Writer, opts Options) (Manifest, error) {
	if st.Dialect() != store.SQLite {
		return Manifest{}, store.ErrNotSQLite
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	status, err := st.MigrationStatus(ctx)
	if err != nil {
		return Manifest{}, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(st.SQLitePath()), ".backup-*")
	if err != nil {
		return Manifest{}, fmt.Errorf("backup: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	snapshot := filepath.Join(tmp, DatabaseName)
	if err := st.SnapshotSQLite(ctx, snapshot); err != nil {
		return Manifest{}, err
	}

	m := Manifest{
		Format: FormatVersion, Version: opts.Version, Schema: status.Current, KeyID: opts.KeyID,
		CreatedAt: now().UTC().Truncate(time.Second), WithArtifacts: opts.ArtifactsDir != "",
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := writeBytes(tw, ManifestName, mb, m.CreatedAt); err != nil {
		return m, err
	}
	if err := writeFile(tw, DatabaseName, snapshot); err != nil {
		return m, err
	}
	if opts.ArtifactsDir != "" {
		err := filepath.WalkDir(opts.ArtifactsDir, func(p string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) && p == opts.ArtifactsDir {
				return fs.SkipAll
			}
			if err != nil || d.IsDir() {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			rel, err := filepath.Rel(opts.ArtifactsDir, p)
			if err != nil {
				return err
			}
			return writeFile(tw, artifactsPrefix+filepath.ToSlash(rel), p)
		})
		if err != nil {
			return m, fmt.Errorf("backup: artifacts: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return m, fmt.Errorf("backup: %w", err)
	}
	if err := gz.Close(); err != nil {
		return m, fmt.Errorf("backup: %w", err)
	}
	return m, nil
}

func writeBytes(tw *tar.Writer, name string, b []byte, mod time.Time) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b)), ModTime: mod, Typeflag: tar.TypeReg}); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	_, err := tw.Write(b)
	return err
}

func writeFile(tw *tar.Writer, name, src string) error {
	f, err := os.Open(src) //nolint:gosec // paths come from the store and the data directory
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("backup: %s: %w", name, err)
	}
	return nil
}

// RestoreOptions control Restore.
type RestoreOptions struct {
	// DatabasePath is the SQLite file to replace.
	DatabasePath string
	// ArtifactsDir receives the artifacts of a backup that has them.
	ArtifactsDir string
	// LatestSchema is the newest migration this binary knows; newer backups are refused.
	LatestSchema int64
	// KeyID is the id of the master key in use, compared with the backup's.
	KeyID string
}

// Restored reports what Restore did.
type Restored struct {
	Manifest Manifest
	// Previous is where the replaced database was moved, "" when there was none.
	Previous string
	// KeyMismatch is true when the backup was taken with another master key: its secrets can
	// only be read with that key.
	KeyMismatch bool
	Artifacts   int
}

// Errors returned by Restore.
var (
	ErrNotABackup   = errors.New("backup: the file is not a Rowbird backup")
	ErrNewerBackup  = errors.New("backup: the backup comes from a newer Rowbird; upgrade before restoring it")
	ErrUnsafeMember = errors.New("backup: the archive holds a path outside its folders")
)

// Restore replaces the database (and adds the artifacts) with the contents of the archive at src.
// The server must be stopped. The current database is kept next to it with a .pre-restore suffix.
func Restore(ctx context.Context, src string, opts RestoreOptions) (*Restored, error) {
	staging, err := os.MkdirTemp(filepath.Dir(opts.DatabasePath), ".restore-*")
	if err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	res := &Restored{}
	var haveManifest, haveDB bool
	err = readArchive(src, func(h *tar.Header, r io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch {
		case h.Name == ManifestName:
			if err := json.NewDecoder(io.LimitReader(r, 1<<16)).Decode(&res.Manifest); err != nil {
				return ErrNotABackup
			}
			haveManifest = true
			return checkManifest(res.Manifest, opts)
		case h.Name == DatabaseName:
			if !haveManifest {
				return ErrNotABackup
			}
			haveDB = true
			return extract(filepath.Join(staging, DatabaseName), r)
		case strings.HasPrefix(h.Name, artifactsPrefix):
			rel := strings.TrimPrefix(h.Name, artifactsPrefix)
			if !haveManifest || rel == "" || !fs.ValidPath(rel) || path.Clean(rel) != rel {
				return ErrUnsafeMember
			}
			res.Artifacts++
			return extract(filepath.Join(staging, "artifacts", filepath.FromSlash(rel)), r)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !haveManifest || !haveDB {
		return nil, ErrNotABackup
	}
	res.KeyMismatch = opts.KeyID != "" && res.Manifest.KeyID != opts.KeyID

	// Move the current database (and its WAL files) aside, then put the restored one in place.
	if _, err := os.Stat(opts.DatabasePath); err == nil {
		res.Previous = opts.DatabasePath + ".pre-restore"
		if _, err := os.Stat(res.Previous); err == nil {
			res.Previous = fmt.Sprintf("%s.pre-restore-%s", opts.DatabasePath, time.Now().UTC().Format("20060102T150405Z"))
		}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Rename(opts.DatabasePath+suffix, res.Previous+suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("restore: move the current database aside: %w", err)
			}
		}
	}
	if err := os.Rename(filepath.Join(staging, DatabaseName), opts.DatabasePath); err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	if res.Artifacts > 0 && opts.ArtifactsDir != "" {
		err := filepath.WalkDir(filepath.Join(staging, "artifacts"), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(filepath.Join(staging, "artifacts"), p)
			dst := filepath.Join(opts.ArtifactsDir, rel)
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return err
			}
			return os.Rename(p, dst) //nolint:gosec // p is inside our own staging directory, extracted without links
		})
		if err != nil {
			return res, fmt.Errorf("restore: the database was restored, but artifacts failed: %w", err)
		}
	}
	return res, nil
}

// ReadManifest returns the manifest of the archive at src.
func ReadManifest(src string) (Manifest, error) {
	var m Manifest
	found := false
	errStop := errors.New("stop")
	err := readArchive(src, func(h *tar.Header, r io.Reader) error {
		if h.Name != ManifestName {
			return ErrNotABackup
		}
		if err := json.NewDecoder(io.LimitReader(r, 1<<16)).Decode(&m); err != nil {
			return ErrNotABackup
		}
		found = true
		return errStop
	})
	if err != nil && !errors.Is(err, errStop) {
		return m, err
	}
	if !found {
		return m, ErrNotABackup
	}
	return m, nil
}

func checkManifest(m Manifest, opts RestoreOptions) error {
	if m.Format < 1 || m.Format > FormatVersion {
		return ErrNotABackup
	}
	if opts.LatestSchema > 0 && m.Schema > opts.LatestSchema {
		return ErrNewerBackup
	}
	return nil
}

func readArchive(src string, fn func(*tar.Header, io.Reader) error) error {
	f, err := os.Open(src) //nolint:gosec // the operator names the backup file
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return ErrNotABackup
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return ErrNotABackup
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if err := fn(h, tr); err != nil {
			return err
		}
	}
}

func extract(dst string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // dst is inside the staging directory
	if err != nil {
		return err
	}
	_, cerr := io.Copy(f, r) //nolint:gosec // a backup is the operator's own file; its size is not bounded
	return errors.Join(cerr, f.Close())
}
