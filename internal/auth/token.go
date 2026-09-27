package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// APIKeyPrefix starts every API key, which makes leaked keys easy to recognize and scan for.
const APIKeyPrefix = "rbk_"

// apiKeyDisplayLen is how many characters after the prefix are stored and shown to identify a key.
const apiKeyDisplayLen = 8

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// HashToken is how every bearer secret (session token, API key, challenge) is stored: SHA-256, hex.
// The secrets have 256 bits of entropy, so a slow hash adds nothing.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewSessionToken returns 32 random bytes, base64url encoded, and its hash.
func NewSessionToken() (token, hash string) {
	b := make([]byte, 32)
	mustRead(b)
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

// NewAPIKey returns a key "rbk_" + 43 base62 characters (256 bits), the short prefix shown in the
// UI, and the hash to store.
func NewAPIKey() (key, displayPrefix, hash string) {
	body := randomString(base62, 43)
	key = APIKeyPrefix + body
	return key, body[:apiKeyDisplayLen], HashToken(key)
}

// LooksLikeAPIKey reports whether s has the shape of an API key.
func LooksLikeAPIKey(s string) bool {
	return strings.HasPrefix(s, APIKeyPrefix) && len(s) == len(APIKeyPrefix)+43
}

// temporaryPasswordAlphabet leaves out characters that are easy to confuse when read aloud or copied.
const temporaryPasswordAlphabet = "23456789abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ"

// NewTemporaryPassword returns a random 16-character password for accounts created or reset by an
// administrator. It satisfies ValidatePassword.
func NewTemporaryPassword() string { return randomString(temporaryPasswordAlphabet, 16) }

// randomString draws n characters uniformly from alphabet using rejection sampling.
func randomString(alphabet string, n int) string {
	// Bytes at or above limit would favor the first characters of the alphabet; they are skipped.
	limit := 256 - 256%len(alphabet)
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	for len(out) < n {
		mustRead(buf)
		for _, b := range buf {
			if int(b) < limit && len(out) < n {
				out = append(out, alphabet[int(b)%len(alphabet)])
			}
		}
	}
	return string(out)
}

func mustRead(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic("auth: system random source failed: " + err.Error())
	}
}
