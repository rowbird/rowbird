// Package webauthntest is a software authenticator for tests: it answers WebAuthn registration and
// assertion options the way a browser with a platform authenticator would, with an ES256 key and
// "none" attestation.
package webauthntest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

var b64 = base64.RawURLEncoding

// Credential is a key the authenticator created.
type Credential struct {
	ID         []byte
	UserHandle []byte
	RPID       string
	Key        *ecdsa.PrivateKey
	// SignCount is sent with the next assertion, after being incremented.
	SignCount uint32
}

// Authenticator holds credentials for one origin.
type Authenticator struct {
	Origin      string
	Credentials []*Credential
	// SkipUV leaves the user-verified flag off, like a security key without a PIN.
	SkipUV bool
}

// New returns an authenticator for pages served from origin (scheme://host[:port]).
func New(origin string) *Authenticator { return &Authenticator{Origin: origin} }

type creationOptions struct {
	Challenge string `json:"challenge"`
	RP        struct {
		ID string `json:"id"`
	} `json:"rp"`
	User struct {
		ID string `json:"id"`
	} `json:"user"`
}

type requestOptions struct {
	Challenge        string `json:"challenge"`
	RPID             string `json:"rpId"`
	AllowCredentials []struct {
		ID string `json:"id"`
	} `json:"allowCredentials"`
}

func (a *Authenticator) flags(attested bool) byte {
	f := byte(0x01) // user present
	if !a.SkipUV {
		f |= 0x04
	}
	f |= 0x08 | 0x10 // backup eligible and backed up, like a synced passkey
	if attested {
		f |= 0x40
	}
	return f
}

func clientData(typ, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}

// Register answers the options of navigator.credentials.create (the publicKey member, as JSON).
func (a *Authenticator) Register(options []byte) (json.RawMessage, *Credential, error) {
	var o creationOptions
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, nil, err
	}
	user, err := b64.DecodeString(o.User.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("user id: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	cred := &Credential{ID: id, UserHandle: user, RPID: o.RP.ID, Key: key}

	cose, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: pad32(key.X.Bytes()), -3: pad32(key.Y.Bytes())})
	if err != nil {
		return nil, nil, err
	}
	rpHash := sha256.Sum256([]byte(o.RP.ID))
	auth := append([]byte{}, rpHash[:]...)
	auth = append(auth, a.flags(true))
	auth = binary.BigEndian.AppendUint32(auth, 0)
	auth = append(auth, make([]byte, 16)...)       // AAGUID
	auth = binary.BigEndian.AppendUint16(auth, 32) // len(id)
	auth = append(auth, id...)
	auth = append(auth, cose...)
	att, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": auth})
	if err != nil {
		return nil, nil, err
	}
	resp, err := json.Marshal(map[string]any{
		"id": b64.EncodeToString(id), "rawId": b64.EncodeToString(id), "type": "public-key",
		"authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientData("webauthn.create", o.Challenge, a.Origin)),
			"attestationObject": b64.EncodeToString(att),
			"transports":        []string{"internal"},
		},
	})
	if err != nil {
		return nil, nil, err
	}
	a.Credentials = append(a.Credentials, cred)
	return resp, cred, nil
}

// Assert answers the options of navigator.credentials.get with the first matching credential (the
// given one when not nil).
func (a *Authenticator) Assert(options []byte, use *Credential) (json.RawMessage, error) {
	var o requestOptions
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, err
	}
	cred := use
	if cred == nil {
		for _, c := range a.Credentials {
			if c.RPID != o.RPID {
				continue
			}
			if len(o.AllowCredentials) == 0 {
				cred = c
				break
			}
			for _, allowed := range o.AllowCredentials {
				if allowed.ID == b64.EncodeToString(c.ID) {
					cred = c
				}
			}
			if cred != nil {
				break
			}
		}
	}
	if cred == nil {
		return nil, fmt.Errorf("no credential for %s", o.RPID)
	}
	cred.SignCount++
	rpHash := sha256.Sum256([]byte(o.RPID))
	auth := append([]byte{}, rpHash[:]...)
	auth = append(auth, a.flags(false))
	auth = binary.BigEndian.AppendUint32(auth, cred.SignCount)
	cd := clientData("webauthn.get", o.Challenge, a.Origin)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, auth...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, cred.Key, digest[:])
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(cred.ID), "rawId": b64.EncodeToString(cred.ID), "type": "public-key",
		"authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(cd),
			"authenticatorData": b64.EncodeToString(auth),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(cred.UserHandle),
		},
	})
}

func pad32(b []byte) []byte {
	if len(b) >= 32 {
		return b
	}
	return append(make([]byte, 32-len(b)), b...)
}
