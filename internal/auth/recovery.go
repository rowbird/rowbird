package auth

import (
	"crypto/subtle"
	"strings"
)

// RecoveryCodeCount is how many one-time recovery codes a user gets (docs/spec/07-security.md).
const RecoveryCodeCount = 10

const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// NewRecoveryCodes returns codes shaped "xxxxx-xxxxx" and their hashes, in the same order.
func NewRecoveryCodes() (codes, hashes []string) {
	for range RecoveryCodeCount {
		raw := randomString(recoveryAlphabet, 10)
		code := raw[:5] + "-" + raw[5:]
		codes = append(codes, code)
		hashes = append(hashes, HashToken(normalizeRecoveryCode(code)))
	}
	return codes, hashes
}

// normalizeRecoveryCode ignores case, spaces and dashes so codes survive being typed by hand.
func normalizeRecoveryCode(code string) string {
	return strings.NewReplacer("-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(code)))
}

// MatchRecoveryCode returns the index of the hash matching code, or -1. It compares against every
// hash so timing does not reveal which one matched.
func MatchRecoveryCode(code string, hashes []string) int {
	h := HashToken(normalizeRecoveryCode(code))
	found := -1
	for i, candidate := range hashes {
		if subtle.ConstantTimeCompare([]byte(h), []byte(candidate)) == 1 {
			found = i
		}
	}
	return found
}
