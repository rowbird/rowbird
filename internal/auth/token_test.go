package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestTokens(t *testing.T) {
	tok, hash := NewSessionToken()
	if len(tok) != 43 || hash != HashToken(tok) || len(hash) != 64 || strings.Contains(hash, tok) {
		t.Fatalf("session token %q hash %q", tok, hash)
	}
	other, _ := NewSessionToken()
	if other == tok {
		t.Fatal("tokens repeat")
	}

	key, prefix, keyHash := NewAPIKey()
	if !LooksLikeAPIKey(key) || !strings.HasPrefix(key, "rbk_"+prefix) || len(prefix) != 8 || keyHash != HashToken(key) {
		t.Fatalf("api key %q prefix %q", key, prefix)
	}
	for _, c := range strings.TrimPrefix(key, "rbk_") {
		if !strings.ContainsRune(base62, c) {
			t.Fatalf("non-base62 character %q", c)
		}
	}
	if LooksLikeAPIKey("rbk_short") || LooksLikeAPIKey(tok) {
		t.Fatal("LooksLikeAPIKey accepts wrong shapes")
	}
}

func TestRandomStringIsUniform(t *testing.T) {
	counts := map[rune]int{}
	s := randomString("ab", 20000)
	for _, c := range s {
		counts[c]++
	}
	if counts['a'] < 9500 || counts['b'] < 9500 {
		t.Fatalf("skewed distribution %v", counts)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes := NewRecoveryCodes()
	if len(codes) != RecoveryCodeCount || len(hashes) != RecoveryCodeCount {
		t.Fatalf("got %d codes", len(codes))
	}
	for i, c := range codes {
		if len(c) != 11 || c[5] != '-' {
			t.Fatalf("code shape %q", c)
		}
		if MatchRecoveryCode(c, hashes) != i {
			t.Fatalf("code %d does not match its hash", i)
		}
	}
	messy := " " + strings.ToUpper(strings.ReplaceAll(codes[3], "-", " ")) + " "
	if MatchRecoveryCode(messy, hashes) != 3 {
		t.Fatalf("%q not normalized", messy)
	}
	if MatchRecoveryCode("aaaaa-aaaaa", hashes) != -1 {
		t.Fatal("unknown code matched")
	}
}

func TestTOTP(t *testing.T) {
	e, err := NewTOTPEnrollment("Rowbird", "ana@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.URL, "otpauth://totp/Rowbird:ana@example.com?") || !strings.HasPrefix(e.QRCodePNG, "data:image/png;base64,") {
		t.Fatalf("enrollment %+v", e.URL)
	}

	now := time.Unix(1_800_000_000, 0)
	step := now.Unix() / 30
	code := func(at time.Time) string {
		c, err := totp.GenerateCode(e.Secret, at)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	cases := []struct {
		name     string
		code     string
		ok       bool
		wantStep int64
	}{
		{"current", code(now), true, step},
		{"previous step", code(now.Add(-30 * time.Second)), true, step - 1},
		{"next step", code(now.Add(30 * time.Second)), true, step + 1},
		{"with spaces", code(now)[:3] + " " + code(now)[3:], true, step},
		{"too old", code(now.Add(-90 * time.Second)), false, 0},
		{"wrong length", "12345", false, 0},
		{"letters", "abcdef", false, 0},
	}
	for _, tc := range cases {
		got, ok := MatchTOTP(e.Secret, tc.code, now)
		if ok != tc.ok || got != tc.wantStep {
			// A random wrong code can collide with a valid one; the fixed time makes this deterministic.
			t.Errorf("%s: ok=%v step=%d, want ok=%v step=%d", tc.name, ok, got, tc.ok, tc.wantStep)
		}
	}
}
