package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rowbird/rowbird/internal/security"
)

// GeneratedKeyFile is the name of the key file written into the data directory when no key is
// configured.
const GeneratedKeyFile = "master.key"

// MasterKeySource describes where the master key comes from, in order of preference: the value of
// ROWBIRD_MASTER_KEY, the file named by ROWBIRD_MASTER_KEY_FILE, or a key file in the data directory
// that is generated on first start.
type MasterKeySource struct {
	Value   security.Secret
	File    string
	DataDir string
}

// MasterKey is the loaded key. Generated is true when this call created the key file, which the
// setup wizard uses to warn the administrator to back it up.
type MasterKey struct {
	Raw       []byte
	Generated bool
	Path      string
}

// LoadMasterKey resolves the master key. Errors never contain key material.
func LoadMasterKey(src MasterKeySource) (*MasterKey, error) {
	switch {
	case src.Value.IsSet():
		raw, err := decodeKey(src.Value.Reveal())
		if err != nil {
			return nil, fmt.Errorf("ROWBIRD_MASTER_KEY: %w", err)
		}
		return &MasterKey{Raw: raw}, nil
	case src.File != "":
		raw, err := readKeyFile(src.File)
		if err != nil {
			return nil, fmt.Errorf("ROWBIRD_MASTER_KEY_FILE: %w", err)
		}
		return &MasterKey{Raw: raw, Path: src.File}, nil
	case src.DataDir != "":
		return loadOrGenerate(filepath.Join(src.DataDir, GeneratedKeyFile))
	}
	return nil, errors.New("no master key source configured")
}

// LoadKey reads a key given as a base64 value or as a file holding one. It returns nil when neither
// is set. Errors never contain key material.
func LoadKey(value security.Secret, file string) ([]byte, error) {
	switch {
	case value.IsSet():
		return decodeKey(value.Reveal())
	case file != "":
		return readKeyFile(file)
	}
	return nil, nil
}

// NewRawKey returns a fresh random key.
func NewRawKey() ([]byte, error) {
	raw := make([]byte, KeySize)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return raw, nil
}

// EncodeKey returns the key as written in key files and ROWBIRD_MASTER_KEY.
func EncodeKey(raw []byte) string { return base64.StdEncoding.EncodeToString(raw) }

// WriteKeyFile replaces the key file at path atomically, readable only by its owner.
func WriteKeyFile(path string, raw []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".master.key-*")
	if err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, werr := tmp.WriteString(EncodeKey(raw) + "\n")
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp.Name(), 0o600)); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	return nil
}

func loadOrGenerate(path string) (*MasterKey, error) {
	raw, err := readKeyFile(path)
	if err == nil {
		return &MasterKey{Raw: raw, Path: path}, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	raw = make([]byte, KeySize)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	// O_EXCL makes two processes starting at once agree on a single key.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path is inside the configured data dir
	if errors.Is(err, fs.ErrExist) {
		return loadOrGenerate(path)
	}
	if err != nil {
		return nil, fmt.Errorf("create master key file: %w", err)
	}
	_, werr := f.WriteString(base64.StdEncoding.EncodeToString(raw) + "\n")
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return nil, fmt.Errorf("write master key file: %w", err)
	}
	return &MasterKey{Raw: raw, Generated: true, Path: path}, nil
}

func readKeyFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the path comes from the operator's configuration
	if err != nil {
		return nil, err
	}
	return decodeKey(string(b))
}

func decodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(s)
	}
	if err != nil {
		return nil, errors.New("master key is not valid base64")
	}
	if len(raw) != KeySize {
		return nil, fmt.Errorf("master key must decode to %d bytes, got %d", KeySize, len(raw))
	}
	return raw, nil
}
