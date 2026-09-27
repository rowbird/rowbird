package keys_test

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/ai"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/keys"
	"github.com/rowbird/rowbird/internal/notify"
	"github.com/rowbird/rowbird/internal/secretconfig"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/testenv"
)

func newKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, crypto.KeySize)
	_, _ = rand.Read(k)
	return k
}

type fixture struct {
	env   *testenv.Env
	ws    uuid.UUID
	plain map[string]string // SecretValue kind/key -> plaintext
}

// seed stores one encrypted value of every kind with the environment's key.
func seed(t *testing.T) *fixture {
	t.Helper()
	e := testenv.New(t)
	f := &fixture{env: e, ws: e.Principal.WorkspaceID, plain: map[string]string{}}
	put := func(v store.SecretValue, aad []byte, plain string) {
		enc, err := e.Keyring.Encrypt([]byte(plain), aad)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Store.SecretValues().Replace(e.Ctx, v, enc); err != nil {
			t.Fatal(err)
		}
		f.plain[v.Kind+"/"+v.Key] = plain
	}
	put(store.SecretValue{Kind: store.SecretUserTOTP, ID: e.Principal.UserID}, auth.TOTPAssociatedData(e.Principal.UserID), "JBSWY3DPEHPK3PXP")
	put(store.SecretValue{Kind: store.SecretConnection, ID: e.Conn.ID}, secretconfig.AssociatedData("connection", e.Conn.ID), `{"password":"db-secret"}`)
	for _, k := range []string{store.SettingHeartbeatURL, ai.SettingSecrets, auth.SettingOIDCSecret} {
		if err := e.Store.Settings().Put(e.Ctx, k, `""`, true); err != nil {
			t.Fatal(err)
		}
		st, _ := e.Store.Settings().Get(e.Ctx, k)
		aad := notify.HeartbeatAssociatedData(f.ws)
		plain := "https://kuma.example.com/api/push/tok"
		switch k {
		case ai.SettingSecrets:
			aad, plain = secretconfig.AssociatedData(ai.SecretsKind, f.ws), `{"api_key":"sk-test"}`
		case auth.SettingOIDCSecret:
			aad, plain = secretconfig.AssociatedData(auth.OIDCSecretKind, f.ws), "oidc-client-secret"
		}
		put(store.SecretValue{Kind: store.SecretSetting, ID: st.ID, WorkspaceID: f.ws, Key: k}, aad, plain)
	}
	return f
}

// check decrypts every stored value with kr and compares it with what was seeded.
func (f *fixture) check(t *testing.T, kr *crypto.Keyring, wantKey string) {
	t.Helper()
	values, err := f.env.Store.SecretValues().List(f.env.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != len(f.plain) {
		t.Fatalf("%d values, want %d", len(values), len(f.plain))
	}
	for _, v := range values {
		if id, _ := crypto.KeyIDOf(v.Value); id != wantKey {
			t.Errorf("%s %s uses key %s, want %s", v.Kind, v.Key, id, wantKey)
		}
		var aad []byte
		switch {
		case v.Kind == store.SecretUserTOTP:
			aad = auth.TOTPAssociatedData(v.ID)
		case v.Kind == store.SecretConnection:
			aad = secretconfig.AssociatedData("connection", v.ID)
		case v.Key == store.SettingHeartbeatURL:
			aad = notify.HeartbeatAssociatedData(v.WorkspaceID)
		case v.Key == auth.SettingOIDCSecret:
			aad = secretconfig.AssociatedData(auth.OIDCSecretKind, v.WorkspaceID)
		default:
			aad = secretconfig.AssociatedData(ai.SecretsKind, v.WorkspaceID)
		}
		got, err := kr.Decrypt(v.Value, aad)
		if err != nil || string(got) != f.plain[v.Kind+"/"+v.Key] {
			t.Errorf("%s %s: %q, %v", v.Kind, v.Key, got, err)
		}
	}
}

func TestRotate(t *testing.T) {
	f := seed(t)
	next := newKey(t)
	nextRing, _ := crypto.NewKeyring(next)

	res, err := keys.Rotate(f.env.Ctx, f.env.Store, f.env.Keyring, next, keys.Options{DryRun: true})
	if err != nil || res.Total() != 5 {
		t.Fatalf("dry run: %+v, %v", res, err)
	}
	f.check(t, f.env.Keyring, f.env.Keyring.PrimaryKeyID())

	res, err = keys.Rotate(f.env.Ctx, f.env.Store, f.env.Keyring, next, keys.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total() != 5 || res.Rotated[store.SecretSetting] != 3 || res.NewKey != crypto.KeyID(next) {
		t.Fatalf("result %+v", res)
	}
	f.check(t, nextRing, nextRing.PrimaryKeyID())

	// A repeated rotation to the same key finds nothing left to do.
	res, err = keys.Rotate(f.env.Ctx, f.env.Store, nextRing, next, keys.Options{})
	if err != nil || res.Total() != 0 || res.Current != 5 {
		t.Fatalf("repeat: %+v, %v", res, err)
	}

	page, err := f.env.Store.SecurityEvents().List(f.env.Ctx, store.SecurityEventFilter{}, store.PageRequest{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range page.Items {
		if e.Type == keys.EventKeysRotated {
			found = true
			b, _ := json.Marshal(e.Meta)
			if strings.Contains(string(b), "sk-test") || strings.Contains(string(b), "db-secret") {
				t.Fatalf("event leaks a secret: %s", b)
			}
		}
	}
	if !found {
		t.Fatal("no keys_rotated event")
	}
}

func TestRotateWithThePreviousKey(t *testing.T) {
	// Instances restarted with the new key and the old one as previous: the keyring in use holds
	// both, and the rotation moves every value to the new key.
	f := seed(t)
	old := f.env.Keyring
	next := newKey(t)
	// Rebuild the old keyring's raw key through a rotation to a known key first.
	known := newKey(t)
	if _, err := keys.Rotate(f.env.Ctx, f.env.Store, old, known, keys.Options{}); err != nil {
		t.Fatal(err)
	}
	both, _ := crypto.NewKeyring(next, known)
	if _, err := keys.Rotate(f.env.Ctx, f.env.Store, both, next, keys.Options{}); err != nil {
		t.Fatal(err)
	}
	nextRing, _ := crypto.NewKeyring(next)
	f.check(t, nextRing, nextRing.PrimaryKeyID())
}

func TestRotateRollsBackOnFailure(t *testing.T) {
	f := seed(t)
	// A secret setting the rotation does not know how to bind stops it before anything changes.
	if err := f.env.Store.Settings().Put(f.env.Ctx, "future_secret", `"v1:x:y:z"`, true); err != nil {
		t.Fatal(err)
	}
	_, err := keys.Rotate(f.env.Ctx, f.env.Store, f.env.Keyring, newKey(t), keys.Options{})
	if err == nil || !strings.Contains(err.Error(), "future_secret") {
		t.Fatalf("err = %v", err)
	}
	_ = f.env.Store.Settings().Delete(f.env.Ctx, "future_secret")
	f.check(t, f.env.Keyring, f.env.Keyring.PrimaryKeyID())
}

func TestRotateRefusesWhileOldInstancesRun(t *testing.T) {
	f := seed(t)
	next := newKey(t)
	now := time.Now()
	in := &store.Instance{ID: "i-1", Hostname: "h", Version: "dev", KeyID: f.env.Keyring.PrimaryKeyID()}
	if err := f.env.Store.System().Heartbeat(f.env.Ctx, in, now); err != nil {
		t.Fatal(err)
	}
	_, err := keys.Rotate(f.env.Ctx, f.env.Store, f.env.Keyring, next, keys.Options{})
	if !errors.Is(err, keys.ErrInstancesRunning) {
		t.Fatalf("err = %v", err)
	}
	// Restarted with the new key, the instance no longer blocks the rotation.
	in.KeyID = crypto.KeyID(next)
	if err := f.env.Store.System().Heartbeat(f.env.Ctx, in, now); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Rotate(f.env.Ctx, f.env.Store, f.env.Keyring, next, keys.Options{}); err != nil {
		t.Fatal(err)
	}
}
