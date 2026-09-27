// Package auth implements identity and access: passwords, sessions, second factors, API keys and
// the service that ties them to the store (docs/spec/07-security.md).
package auth

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Password length limits. The maximum bounds the work an attacker can force on the hasher.
const (
	MinPasswordLength = 10
	MaxPasswordLength = 256
)

// Argon2Params configure argon2id. Memory is in KiB.
type Argon2Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// DefaultArgon2Params are the values from docs/spec/07-security.md: 64 MiB, 3 passes, 2 lanes.
var DefaultArgon2Params = Argon2Params{Memory: 64 * 1024, Time: 3, Threads: 2, SaltLen: 16, KeyLen: 32}

// PasswordHasher hashes and verifies passwords with argon2id in PHC string format. A semaphore
// bounds concurrent hashes, since each one allocates Params.Memory.
type PasswordHasher struct {
	params Argon2Params
	sem    chan struct{}
	dummy  string
}

// NewPasswordHasher returns a hasher allowing up to concurrency hashes at a time.
func NewPasswordHasher(params Argon2Params, concurrency int) *PasswordHasher {
	if concurrency < 1 {
		concurrency = 1
	}
	h := &PasswordHasher{params: params, sem: make(chan struct{}, concurrency)}
	h.dummy = h.encode(make([]byte, params.SaltLen), argon2.IDKey([]byte("rowbird-dummy"), make([]byte, params.SaltLen), params.Time, params.Memory, params.Threads, params.KeyLen), params)
	return h
}

func (h *PasswordHasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *PasswordHasher) release() { <-h.sem }

// Hash returns the PHC encoding of password with a fresh salt.
func (h *PasswordHasher) Hash(ctx context.Context, password string) (string, error) {
	salt := make([]byte, h.params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	key := argon2.IDKey([]byte(password), salt, h.params.Time, h.params.Memory, h.params.Threads, h.params.KeyLen)
	return h.encode(salt, key, h.params), nil
}

// Verify checks password against an encoded hash. needsRehash is true when the hash was made with
// different parameters, so the caller can upgrade it after a successful login.
func (h *PasswordHasher) Verify(ctx context.Context, password, encoded string) (ok, needsRehash bool, err error) {
	p, salt, want, err := decodePHC(encoded)
	if err != nil {
		return false, false, err
	}
	if err := h.acquire(ctx); err != nil {
		return false, false, err
	}
	defer h.release()
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(want))) //nolint:gosec // key length comes from our own hash
	ok = subtle.ConstantTimeCompare(got, want) == 1
	stored := p.withLengths(uint32(len(salt)), uint32(len(want))) //nolint:gosec // lengths come from our own hash
	return ok, ok && stored != h.params, nil
}

// VerifyDummy spends the same time as Verify without a real hash. Login calls it for unknown or
// passwordless accounts so response time does not reveal whether an account exists.
func (h *PasswordHasher) VerifyDummy(ctx context.Context, password string) {
	_, _, _ = h.Verify(ctx, password, h.dummy)
}

func (p Argon2Params) withLengths(salt, key uint32) Argon2Params {
	p.SaltLen, p.KeyLen = salt, key
	return p
}

var b64 = base64.RawStdEncoding

func (h *PasswordHasher) encode(salt, key []byte, p Argon2Params) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads,
		b64.EncodeToString(salt), b64.EncodeToString(key))
}

var errBadHash = errors.New("auth: malformed password hash")

func decodePHC(s string) (Argon2Params, []byte, []byte, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return Argon2Params{}, nil, nil, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Argon2Params{}, nil, nil, errBadHash
	}
	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil || p.Threads == 0 || p.Time == 0 {
		return Argon2Params{}, nil, nil, errBadHash
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return Argon2Params{}, nil, nil, errBadHash
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return Argon2Params{}, nil, nil, errBadHash
	}
	return p, salt, key, nil
}

// common_passwords.txt is derived from SecLists (MIT): the NCSC 100k list, keeping entries of
// 10 to 256 characters, lowercased, deduplicated, without hashcat "$hex[" artifacts and without entries shaped like email addresses (they
// can identify people). Lines
// starting with "# " are comments.
//
//go:embed common_passwords.txt
var commonPasswordsFile string

var commonPasswords = func() map[string]struct{} {
	set := make(map[string]struct{}, 10000)
	sc := bufio.NewScanner(strings.NewReader(commonPasswordsFile))
	for sc.Scan() {
		if line := sc.Text(); line != "" && !strings.HasPrefix(line, "# ") {
			set[line] = struct{}{}
		}
	}
	return set
}()

// Validation codes returned in PolicyError (docs/spec/05-api.md, field errors).
const (
	CodeTooShort       = "validation.too_short"
	CodeTooLong        = "validation.too_long"
	CodePasswordCommon = "validation.password_common"
)

// PolicyError explains why a password was rejected.
type PolicyError struct{ Code string }

func (e *PolicyError) Error() string { return "auth: password rejected: " + e.Code }

// ValidatePassword applies the password policy: length in characters (not bytes), not a common
// password, and not the account's own email.
func ValidatePassword(password, email string) error {
	n := utf8.RuneCountInString(password)
	switch {
	case n < MinPasswordLength:
		return &PolicyError{Code: CodeTooShort}
	case n > MaxPasswordLength:
		return &PolicyError{Code: CodeTooLong}
	}
	lower := strings.ToLower(password)
	if _, common := commonPasswords[lower]; common {
		return &PolicyError{Code: CodePasswordCommon}
	}
	if email != "" && lower == strings.ToLower(strings.TrimSpace(email)) {
		return &PolicyError{Code: CodePasswordCommon}
	}
	return nil
}
