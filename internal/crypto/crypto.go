// Package crypto encrypts secrets at rest with AES-256-GCM (docs/spec/07-security.md).
//
// Ciphertexts are self-describing strings, "v1:<key_id>:<nonce_b64>:<ct_b64>", so a keyring holding
// the current key and older keys can decrypt values written before a key rotation.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// KeySize is the master key length in bytes (AES-256).
const KeySize = 32

const formatVersion = "v1"

var b64 = base64.RawStdEncoding

// Errors returned by Decrypt. They never include key material or plaintext.
var (
	ErrMalformed  = errors.New("crypto: malformed ciphertext")
	ErrUnknownKey = errors.New("crypto: ciphertext was encrypted with an unknown key")
	ErrDecrypt    = errors.New("crypto: decryption failed")
)

// KeyID derives the public identifier of a key: the first 8 hex characters of a domain-separated
// SHA-256 digest. It identifies the key without revealing anything useful about it.
func KeyID(key []byte) string {
	sum := sha256.Sum256(append([]byte("rowbird-key-id:"), key...))
	return hex.EncodeToString(sum[:4])
}

type key struct {
	id      string
	aead    cipher.AEAD
	derived []byte // HMAC key from which DeriveKey derives purpose-specific keys
}

func newKey(raw []byte) (*key, error) {
	if len(raw) != KeySize {
		return nil, fmt.Errorf("crypto: key must be %d bytes, got %d", KeySize, len(raw))
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("crypto: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: %w", err)
	}
	mac := hmac.New(sha256.New, raw)
	mac.Write([]byte("rowbird-derive-root"))
	return &key{id: KeyID(raw), aead: aead, derived: mac.Sum(nil)}, nil
}

// Keyring encrypts with its primary key and decrypts with any key it holds.
type Keyring struct {
	primary *key
	keys    map[string]*key
}

// NewKeyring builds a keyring. Older keys are accepted for decryption only, which is what a key
// rotation needs while values are being re-encrypted.
func NewKeyring(primary []byte, older ...[]byte) (*Keyring, error) {
	p, err := newKey(primary)
	if err != nil {
		return nil, err
	}
	kr := &Keyring{primary: p, keys: map[string]*key{p.id: p}}
	for _, raw := range older {
		k, err := newKey(raw)
		if err != nil {
			return nil, err
		}
		if _, dup := kr.keys[k.id]; !dup {
			kr.keys[k.id] = k
		}
	}
	return kr, nil
}

// DeriveKey returns a 32-byte key for one purpose (for example "csrf"), derived from the primary
// key with HMAC-SHA256. Different purposes yield independent keys, and the master key itself is
// never used directly outside encryption.
func (kr *Keyring) DeriveKey(purpose string) []byte {
	mac := hmac.New(sha256.New, kr.primary.derived)
	mac.Write([]byte(purpose))
	return mac.Sum(nil)
}

// PrimaryKeyID returns the id of the key used by Encrypt.
func (kr *Keyring) PrimaryKeyID() string { return kr.primary.id }

// Encrypt seals plaintext with the primary key. The optional associated data is authenticated but
// not stored; pass the same value to Decrypt (for example "connection:<id>:secrets") so that a
// ciphertext copied to another row fails to decrypt.
func (kr *Keyring) Encrypt(plaintext, associatedData []byte) (string, error) {
	nonce := make([]byte, kr.primary.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("crypto: read nonce: %w", err)
	}
	ct := kr.primary.aead.Seal(nil, nonce, plaintext, associatedData)
	return strings.Join([]string{formatVersion, kr.primary.id, b64.EncodeToString(nonce), b64.EncodeToString(ct)}, ":"), nil
}

// Decrypt opens a value produced by Encrypt with any key in the keyring.
func (kr *Keyring) Decrypt(ciphertext string, associatedData []byte) ([]byte, error) {
	id, nonce, ct, err := parse(ciphertext)
	if err != nil {
		return nil, err
	}
	k, ok := kr.keys[id]
	if !ok {
		return nil, ErrUnknownKey
	}
	if len(nonce) != k.aead.NonceSize() {
		return nil, ErrMalformed
	}
	pt, err := k.aead.Open(nil, nonce, ct, associatedData)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// KeyIDOf returns the key id recorded in a ciphertext, used by rotation to find stale values.
func KeyIDOf(ciphertext string) (string, error) {
	id, _, _, err := parse(ciphertext)
	return id, err
}

func parse(s string) (id string, nonce, ct []byte, err error) {
	parts := strings.Split(s, ":")
	if len(parts) != 4 || parts[0] != formatVersion || parts[1] == "" {
		return "", nil, nil, ErrMalformed
	}
	if nonce, err = b64.DecodeString(parts[2]); err != nil {
		return "", nil, nil, ErrMalformed
	}
	if ct, err = b64.DecodeString(parts[3]); err != nil {
		return "", nil, nil, ErrMalformed
	}
	return parts[1], nonce, ct, nil
}
