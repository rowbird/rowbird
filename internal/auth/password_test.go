package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// testParams keep tests fast; production uses DefaultArgon2Params.
var testParams = Argon2Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

func TestHashAndVerify(t *testing.T) {
	h := NewPasswordHasher(testParams, 2)
	ctx := t.Context()
	encoded, err := h.Hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("unexpected encoding %q", encoded)
	}
	if strings.Contains(encoded, "correct horse") {
		t.Fatal("hash contains the password")
	}
	again, _ := h.Hash(ctx, "correct horse battery")
	if again == encoded {
		t.Fatal("salt is not random")
	}

	ok, rehash, err := h.Verify(ctx, "correct horse battery", encoded)
	if err != nil || !ok || rehash {
		t.Fatalf("verify: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if ok, _, _ := h.Verify(ctx, "correct horse batterY", encoded); ok {
		t.Fatal("wrong password accepted")
	}
}

func TestRehashWhenParametersChange(t *testing.T) {
	old := NewPasswordHasher(testParams, 1)
	encoded, _ := old.Hash(t.Context(), "correct horse battery")

	stronger := testParams
	stronger.Time = 2
	ok, rehash, err := NewPasswordHasher(stronger, 1).Verify(t.Context(), "correct horse battery", encoded)
	if err != nil || !ok || !rehash {
		t.Fatalf("ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if _, rehash, _ := NewPasswordHasher(stronger, 1).Verify(t.Context(), "wrong password!", encoded); rehash {
		t.Fatal("rehash suggested for a failed verification")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	h := NewPasswordHasher(testParams, 1)
	for _, bad := range []string{
		"", "plain", "$argon2i$v=19$m=64,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=18$m=64,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=19$m=64,t=0,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=64,t=1,p=1$!!!$a2V5", "$argon2id$v=19$m=64,t=1,p=1$c2FsdA$",
	} {
		if _, _, err := h.Verify(t.Context(), "x", bad); !errors.Is(err, errBadHash) {
			t.Errorf("%q: got %v", bad, err)
		}
	}
}

func TestHasherBoundsConcurrencyAndHonorsContext(t *testing.T) {
	h := NewPasswordHasher(testParams, 1)
	h.sem <- struct{}{} // occupy the only slot
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := h.Hash(ctx, "whatever-password"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	<-h.sem

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := h.Hash(t.Context(), "parallel-password"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func TestVerifyDummyDoesNotPanic(t *testing.T) {
	NewPasswordHasher(testParams, 1).VerifyDummy(t.Context(), "anything")
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		password, email, want string
	}{
		{"short", "", CodeTooShort},
		{"ninechars", "", CodeTooShort},
		{"exactly10!", "", ""},
		{strings.Repeat("x", 256), "", ""},
		{strings.Repeat("x", 257), "", CodeTooLong},
		{"çãõéíúàêôü", "", ""}, // 10 characters, 20 bytes
		{"çãõéíúàêô", "", CodeTooShort},
		{"1234567890", "", CodePasswordCommon},
		{"Password123", "", CodePasswordCommon},
		{"QWERTYUIOP", "", CodePasswordCommon},
		{"Ana@Example.com", " ana@example.com", CodePasswordCommon},
		{"a sensible passphrase", "ana@example.com", ""},
	}
	for _, tc := range cases {
		err := ValidatePassword(tc.password, tc.email)
		var pe *PolicyError
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%q rejected: %v", tc.password, err)
		case tc.want != "" && (!errors.As(err, &pe) || pe.Code != tc.want):
			t.Errorf("%q: got %v, want %s", tc.password, err, tc.want)
		}
	}
	if len(commonPasswords) < 5000 {
		t.Errorf("common password list has only %d entries", len(commonPasswords))
	}
}

func TestTemporaryPasswordsPassPolicy(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		p := NewTemporaryPassword()
		if err := ValidatePassword(p, ""); err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		if seen[p] {
			t.Fatal("duplicate temporary password")
		}
		seen[p] = true
	}
}
