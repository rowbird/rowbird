package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/notify"
	"github.com/rowbird/rowbird/internal/store"
)

func TestKeysRotateGeneratedKey(t *testing.T) {
	e := newCLIEnv(t)
	svc, st := e.service()
	if _, err := svc.Setup(t.Context(), auth.SetupInput{Email: "admin@example.com", Name: "Admin", Password: "admin passphrase 1", Locale: "en", Timezone: "UTC"}, auth.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	ws, err := st.Workspaces().GetBySlug(t.Context(), auth.DefaultWorkspaceSlug)
	if err != nil {
		t.Fatal(err)
	}
	ctx := store.WithWorkspace(t.Context(), ws.ID)
	keyPath := filepath.Join(e.dataDir, crypto.GeneratedKeyFile)
	old, err := crypto.LoadMasterKey(crypto.MasterKeySource{DataDir: e.dataDir})
	if err != nil {
		t.Fatal(err)
	}
	oldRing, _ := crypto.NewKeyring(old.Raw)
	const url = "https://kuma.example.com/api/push/secret-token"
	enc, _ := oldRing.Encrypt([]byte(url), notify.HeartbeatAssociatedData(ws.ID))
	b, _ := json.Marshal(enc)
	if err := st.Settings().Put(ctx, store.SettingHeartbeatURL, string(b), true); err != nil {
		t.Fatal(err)
	}

	r := e.run("", "keys", "rotate", "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, "would encrypt 1 values") {
		t.Fatalf("dry run: %+v", r)
	}
	if now, _ := crypto.LoadMasterKey(crypto.MasterKeySource{DataDir: e.dataDir}); crypto.KeyID(now.Raw) != oldRing.PrimaryKeyID() {
		t.Fatal("a dry run replaced the key file")
	}

	r = e.run("", "keys", "rotate")
	if r.code != 0 || !strings.Contains(r.stdout, "encrypted 1 values") || strings.Contains(r.stdout+r.stderr, "secret-token") {
		t.Fatalf("rotate: %+v", r)
	}
	next, err := crypto.LoadMasterKey(crypto.MasterKeySource{DataDir: e.dataDir})
	if err != nil || crypto.KeyID(next.Raw) == oldRing.PrimaryKeyID() {
		t.Fatalf("key file not replaced: %v", err)
	}
	if prev, err := crypto.LoadKey("", keyPath+".previous"); err != nil || crypto.KeyID(prev) != oldRing.PrimaryKeyID() {
		t.Fatalf("previous key not kept: %v", err)
	}
	if _, err := os.Stat(keyPath + ".next"); !os.IsNotExist(err) {
		t.Fatalf("staging key file left behind: %v", err)
	}
	nextRing, _ := crypto.NewKeyring(next.Raw)
	stored, _ := st.Settings().Get(ctx, store.SettingHeartbeatURL)
	var ct string
	_ = json.Unmarshal([]byte(stored.Value), &ct)
	if got, err := nextRing.Decrypt(ct, notify.HeartbeatAssociatedData(ws.ID)); err != nil || string(got) != url {
		t.Fatalf("heartbeat after rotation: %q, %v", got, err)
	}
}

func TestKeysRotateConfiguredKeyNeedsTheNewKey(t *testing.T) {
	e := newCLIEnv(t)
	k, _ := crypto.NewRawKey()
	e.environ = append(e.environ, "ROWBIRD_MASTER_KEY="+crypto.EncodeKey(k))
	if r := e.run("", "keys", "rotate"); r.code != 1 || !strings.Contains(r.stderr, "--new-key-file") {
		t.Fatalf("%+v", r)
	}
	same := filepath.Join(t.TempDir(), "k")
	if err := crypto.WriteKeyFile(same, k); err != nil {
		t.Fatal(err)
	}
	if r := e.run("", "keys", "rotate", "--new-key-file", same); r.code != 1 || !strings.Contains(r.stderr, "already in use") {
		t.Fatalf("%+v", r)
	}
	next, _ := crypto.NewRawKey()
	nextFile := filepath.Join(t.TempDir(), "next")
	_ = crypto.WriteKeyFile(nextFile, next)
	if r := e.run("", "keys", "rotate", "--new-key-file", nextFile); r.code != 0 || !strings.Contains(r.stdout, "configure the new key") {
		t.Fatalf("%+v", r)
	}
}
