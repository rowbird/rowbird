package crypto

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

func randomKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestRoundTrip(t *testing.T) {
	kr, err := NewKeyring(randomKey(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, pt := range [][]byte{[]byte("password"), {}, bytes.Repeat([]byte{0xff}, 4096)} {
		ct, err := kr.Encrypt(pt, []byte("aad"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(ct, "v1:"+kr.PrimaryKeyID()+":") {
			t.Errorf("unexpected format %q", ct)
		}
		got, err := kr.Decrypt(ct, []byte("aad"))
		if err != nil || !bytes.Equal(got, pt) {
			t.Errorf("round trip failed: %v", err)
		}
	}
}

func TestNonceIsFreshEveryTime(t *testing.T) {
	kr, _ := NewKeyring(randomKey(t))
	a, _ := kr.Encrypt([]byte("same"), nil)
	b, _ := kr.Encrypt([]byte("same"), nil)
	if a == b {
		t.Fatal("two encryptions of the same plaintext are identical")
	}
}

func TestTamperingIsDetected(t *testing.T) {
	kr, _ := NewKeyring(randomKey(t))
	ct, _ := kr.Encrypt([]byte("secret"), []byte("connection:1"))
	parts := strings.Split(ct, ":")

	flip := func(s string) string {
		b := []byte(s)
		if b[0] == 'A' {
			b[0] = 'B'
		} else {
			b[0] = 'A'
		}
		return string(b)
	}
	cases := []struct {
		name string
		ct   string
		aad  string
		want error
	}{
		{"ciphertext", strings.Join([]string{parts[0], parts[1], parts[2], flip(parts[3])}, ":"), "connection:1", ErrDecrypt},
		{"nonce", strings.Join([]string{parts[0], parts[1], flip(parts[2]), parts[3]}, ":"), "connection:1", ErrDecrypt},
		{"associated data", ct, "connection:2", ErrDecrypt},
		{"key id", strings.Join([]string{parts[0], "00000000", parts[2], parts[3]}, ":"), "connection:1", ErrUnknownKey},
		{"version", "v2" + ct[2:], "connection:1", ErrMalformed},
		{"missing part", strings.Join(parts[:3], ":"), "connection:1", ErrMalformed},
		{"bad base64", parts[0] + ":" + parts[1] + ":!!!:" + parts[3], "connection:1", ErrMalformed},
		{"short nonce", parts[0] + ":" + parts[1] + ":AAAA:" + parts[3], "connection:1", ErrMalformed},
		{"empty", "", "", ErrMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := kr.Decrypt(tc.ct, []byte(tc.aad))
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestOlderKeysDecryptOnly(t *testing.T) {
	oldKey, newKey := randomKey(t), randomKey(t)
	oldRing, _ := NewKeyring(oldKey)
	ct, _ := oldRing.Encrypt([]byte("legacy"), nil)

	rotated, err := NewKeyring(newKey, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := rotated.Decrypt(ct, nil)
	if err != nil || string(pt) != "legacy" {
		t.Fatalf("decrypt with older key: %v", err)
	}
	fresh, _ := rotated.Encrypt([]byte("x"), nil)
	if id, _ := KeyIDOf(fresh); id != KeyID(newKey) {
		t.Errorf("encrypted with %s, want primary %s", id, KeyID(newKey))
	}
	if id, _ := KeyIDOf(ct); id != KeyID(oldKey) {
		t.Errorf("KeyIDOf = %s", id)
	}
}

func TestKeySizeIsEnforced(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := NewKeyring(make([]byte, n)); err == nil {
			t.Errorf("accepted %d-byte key", n)
		}
	}
	if _, err := NewKeyring(randomKey(t), make([]byte, 5)); err == nil {
		t.Error("accepted invalid older key")
	}
}

func TestKeyIDIsStableAndDistinct(t *testing.T) {
	a, b := randomKey(t), randomKey(t)
	again := append([]byte(nil), a...)
	if KeyID(a) != KeyID(again) || KeyID(a) == KeyID(b) || len(KeyID(a)) != 8 {
		t.Fatalf("bad key ids %s %s", KeyID(a), KeyID(b))
	}
}

func TestDeriveKey(t *testing.T) {
	raw := randomKey(t)
	a, _ := NewKeyring(raw)
	b, _ := NewKeyring(raw)
	other, _ := NewKeyring(randomKey(t))
	if !bytes.Equal(a.DeriveKey("csrf"), b.DeriveKey("csrf")) {
		t.Fatal("derivation is not deterministic")
	}
	if bytes.Equal(a.DeriveKey("csrf"), a.DeriveKey("links")) || bytes.Equal(a.DeriveKey("csrf"), other.DeriveKey("csrf")) {
		t.Fatal("derived keys collide across purposes or master keys")
	}
	if len(a.DeriveKey("csrf")) != 32 || bytes.Contains(a.DeriveKey("csrf"), raw) {
		t.Fatal("derived key has the wrong size or exposes the master key")
	}
}
