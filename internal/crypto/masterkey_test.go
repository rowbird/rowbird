package crypto

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/security"
)

func TestLoadFromValue(t *testing.T) {
	raw := randomKey(t)
	for name, enc := range map[string]string{
		"padded":     base64.StdEncoding.EncodeToString(raw),
		"unpadded":   base64.RawStdEncoding.EncodeToString(raw),
		"whitespace": "  " + base64.StdEncoding.EncodeToString(raw) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			mk, err := LoadMasterKey(MasterKeySource{Value: security.Secret(enc), DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(mk.Raw, raw) || mk.Generated {
				t.Fatal("wrong key or marked as generated")
			}
		})
	}
}

func TestValueTakesPrecedenceOverDataDir(t *testing.T) {
	dir := t.TempDir()
	raw := randomKey(t)
	if _, err := LoadMasterKey(MasterKeySource{Value: security.Secret(base64.StdEncoding.EncodeToString(raw)), DataDir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, GeneratedKeyFile)); !os.IsNotExist(err) {
		t.Fatal("a key file was generated although a key was configured")
	}
}

func TestLoadFromFile(t *testing.T) {
	raw := randomKey(t)
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(raw)+"\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mk, err := LoadMasterKey(MasterKeySource{File: path})
	if err != nil || !bytes.Equal(mk.Raw, raw) {
		t.Fatalf("load from file: %v", err)
	}
	if _, err := LoadMasterKey(MasterKeySource{File: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing key file must be an error, not a silent generation")
	}
}

func TestGenerateThenReuse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	first, err := LoadMasterKey(MasterKeySource{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Generated || len(first.Raw) != KeySize {
		t.Fatalf("expected a generated key, got %+v", first.Generated)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(first.Path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("key file permissions %o, want 600", perm)
		}
	}
	second, err := LoadMasterKey(MasterKeySource{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if second.Generated || !bytes.Equal(first.Raw, second.Raw) {
		t.Fatal("second load did not reuse the generated key")
	}
}

func TestInvalidKeysDoNotLeak(t *testing.T) {
	const leaky = "definitely-not-a-valid-key-material"
	short := base64.StdEncoding.EncodeToString([]byte("too short"))
	for _, v := range []string{leaky, short} {
		_, err := LoadMasterKey(MasterKeySource{Value: security.Secret(v)})
		if err == nil {
			t.Fatalf("accepted invalid key %q", v)
		}
		if strings.Contains(err.Error(), v) {
			t.Errorf("error leaks the key: %v", err)
		}
	}
}
