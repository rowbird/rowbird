// Package common holds what network connectors share: the SSH tunnel section of the configuration
// schema, its translations and the dial function that routes through the tunnel.
package common

import (
	"context"
	"embed"
	"io"

	"github.com/rowbird/rowbird/internal/connector/sshtunnel"
	"github.com/rowbird/rowbird/internal/plugin"
)

// Group names used by connector schemas.
const (
	GroupConnection = "connection"
	GroupTLS        = "tls"
	GroupSSH        = "ssh"
	GroupAdvanced   = "advanced"
)

var sshOn = &plugin.ShowIf{Field: "ssh_enabled", In: []any{true}}

// SSHFields is the SSH tunnel section, appended to every network connector's schema.
func SSHFields() []plugin.Field {
	return []plugin.Field{
		{Key: "ssh_enabled", Type: plugin.TypeBoolean, Default: false, Group: GroupSSH, Label: "plugin.common.ssh_enabled.label", Help: "plugin.common.ssh_enabled.help"},
		{Key: "ssh_host", Type: plugin.TypeString, Required: true, Format: "hostname", MaxLength: 255, Group: GroupSSH, ShowIf: sshOn, Label: "plugin.common.ssh_host.label"},
		{Key: "ssh_port", Type: plugin.TypeInteger, Default: 22, Minimum: plugin.Float(1), Maximum: plugin.Float(65535), Group: GroupSSH, ShowIf: sshOn, Label: "plugin.common.ssh_port.label"},
		{Key: "ssh_user", Type: plugin.TypeString, Required: true, MaxLength: 255, Group: GroupSSH, ShowIf: sshOn, Label: "plugin.common.ssh_user.label"},
		{Key: "ssh_auth", Type: plugin.TypeString, Enum: []string{sshtunnel.AuthKey, sshtunnel.AuthPassword}, Default: sshtunnel.AuthKey, Group: GroupSSH, ShowIf: sshOn, Label: "plugin.common.ssh_auth.label"},
		{Key: "ssh_password", Type: plugin.TypeString, Required: true, Secret: true, Group: GroupSSH, ShowIf: &plugin.ShowIf{Field: "ssh_auth", In: []any{sshtunnel.AuthPassword}}, Label: "plugin.common.ssh_password.label"},
		{Key: "ssh_private_key", Type: plugin.TypeString, Required: true, Secret: true, Multiline: true, Format: "pem", Group: GroupSSH, ShowIf: &plugin.ShowIf{Field: "ssh_auth", In: []any{sshtunnel.AuthKey}}, Label: "plugin.common.ssh_private_key.label", Help: "plugin.common.ssh_private_key.help"},
		{Key: "ssh_passphrase", Type: plugin.TypeString, Secret: true, Group: GroupSSH, ShowIf: &plugin.ShowIf{Field: "ssh_auth", In: []any{sshtunnel.AuthKey}}, Label: "plugin.common.ssh_passphrase.label"},
		{Key: "ssh_host_key", Type: plugin.TypeString, Format: "fingerprint", MaxLength: 100, Group: GroupSSH, ShowIf: sshOn, Label: "plugin.common.ssh_host_key.label", Help: "plugin.common.ssh_host_key.help"},
	}
}

//go:embed locales/*.json
var locales embed.FS

// Messages are the translations of the shared fields and groups.
func Messages() plugin.Messages { return plugin.MustLoadMessages(locales) }

// Dial returns the dial function a connector must use: through the SSH tunnel when the
// configuration enables it, otherwise base (the policy dialer). The closer releases the tunnel.
func Dial(ctx context.Context, values map[string]any, base plugin.DialFunc) (plugin.DialFunc, io.Closer, error) {
	if on, _ := values["ssh_enabled"].(bool); !on {
		return base, io.NopCloser(nil), nil
	}
	tun, err := sshtunnel.Open(ctx, sshtunnel.Config{
		Host:        Str(values, "ssh_host"),
		Port:        Int(values, "ssh_port", 22),
		User:        Str(values, "ssh_user"),
		Auth:        Str(values, "ssh_auth"),
		Password:    Str(values, "ssh_password"),
		PrivateKey:  Str(values, "ssh_private_key"),
		Passphrase:  Str(values, "ssh_passphrase"),
		Fingerprint: Str(values, "ssh_host_key"),
	}, base)
	if err != nil {
		return nil, nil, err
	}
	return tun.Dial, tun, nil
}

// Str reads a string value.
func Str(values map[string]any, key string) string {
	s, _ := values[key].(string)
	return s
}

// Int reads an integer value (validated values hold int64).
func Int(values map[string]any, key string, def int) int {
	switch v := values[key].(type) {
	case int64:
		return int(v)
	case int:
		return v
	case float64:
		return int(v)
	}
	return def
}

// Bool reads a boolean value.
func Bool(values map[string]any, key string) bool {
	b, _ := values[key].(bool)
	return b
}
