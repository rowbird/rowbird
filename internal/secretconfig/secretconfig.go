// Package secretconfig handles plugin configurations that hold secrets, for every entity that
// stores one (connections, channels): validation against the plugin's schema, secret fields split
// out and encrypted with the entity's id as associated data, the placeholder the API returns for a
// stored secret, and removing secret values from driver and service error messages
// (docs/spec/07-security.md).
package secretconfig

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/logging"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/security"
)

// Codec encrypts and decrypts the secrets of one kind of entity.
type Codec struct {
	keyring *crypto.Keyring
	kind    string
}

// New returns a codec for entities of kind ("connection", "channel"). The kind is part of the
// associated data, so a ciphertext cannot be moved to another kind of entity.
func New(kr *crypto.Keyring, kind string) *Codec { return &Codec{keyring: kr, kind: kind} }

func (c *Codec) aad(id uuid.UUID) []byte { return AssociatedData(c.kind, id) }

// AssociatedData is what a codec of kind binds the secrets of entity id to.
func AssociatedData(kind string, id uuid.UUID) []byte {
	return []byte(kind + ":" + id.String() + ":secrets")
}

// Seal splits validated values into plain configuration and encrypted secrets (nil when there are
// none).
func (c *Codec) Seal(id uuid.UUID, schema *plugin.Schema, validated map[string]any) (map[string]any, *string, error) {
	cfg, secrets := schema.Split(validated)
	if len(secrets) == 0 {
		return cfg, nil, nil
	}
	b, err := json.Marshal(secrets)
	if err != nil {
		return nil, nil, err
	}
	enc, err := c.keyring.Encrypt(b, c.aad(id))
	if err != nil {
		return nil, nil, err
	}
	return cfg, &enc, nil
}

// Secrets decrypts an entity's secrets.
func (c *Codec) Secrets(id uuid.UUID, enc *string) (map[string]any, error) {
	out := map[string]any{}
	if enc == nil || *enc == "" {
		return out, nil
	}
	b, err := c.keyring.Decrypt(*enc, c.aad(id))
	if err != nil {
		return nil, fmt.Errorf("secretconfig: decrypt secrets of %s %s: %w", c.kind, id, err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("secretconfig: decode secrets: %w", err)
	}
	return out, nil
}

// Values rebuilds the full configuration: plain settings plus decrypted secrets, with the schema's
// types restored (JSON turns integers into float64).
func (c *Codec) Values(id uuid.UUID, schema *plugin.Schema, cfg map[string]any, enc *string) (map[string]any, error) {
	secrets, err := c.Secrets(id, enc)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for k, v := range cfg {
		out[k] = v
	}
	for k, v := range secrets {
		out[k] = v
	}
	if schema != nil {
		if validated, err := schema.Validate(out); err == nil {
			return validated, nil
		}
	}
	return out, nil
}

// IsPlaceholder reports whether v is the {"configured": true} object the API returns for a stored
// secret.
func IsPlaceholder(v any) bool {
	m, ok := v.(map[string]any)
	return ok && m["configured"] == true
}

// Configured reports which secret fields hold a value.
func Configured(schema *plugin.Schema, secrets map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, k := range schema.SecretKeys() {
		if v, ok := secrets[k]; ok && v != nil && v != "" {
			out[k] = true
		}
	}
	return out
}

// Resolve applies the placeholder rules to incoming values: for a secret field, absent or the
// placeholder keeps the stored value, null removes it, a string replaces it.
func Resolve(schema *plugin.Schema, in map[string]any, existing map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	for _, key := range schema.SecretKeys() {
		v, present := in[key]
		switch {
		case !present || IsPlaceholder(v):
			if old, ok := existing[key]; ok {
				out[key] = old
			} else {
				delete(out, key)
			}
		case v == nil:
			delete(out, key)
		}
	}
	return out
}

// Scrub removes a configuration's secret values from a message before it is logged or shown:
// drivers and services are free to echo parts of their settings in errors.
func Scrub(msg string, schema *plugin.Schema, values map[string]any) string {
	for _, k := range schema.SecretKeys() {
		if v, _ := values[k].(string); len(v) >= 3 {
			msg = strings.ReplaceAll(msg, v, security.Redacted)
		}
	}
	return logging.RedactString(msg)
}
