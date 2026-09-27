package common

import (
	"crypto/tls"
	"crypto/x509"
	"errors"

	"github.com/rowbird/rowbird/internal/plugin"
)

// TLSFields are the certificate fields shared by network connectors. showIf lists the TLS modes in
// which they apply.
func TLSFields(modeField string, showIf []any) []plugin.Field {
	cond := &plugin.ShowIf{Field: modeField, In: showIf}
	return []plugin.Field{
		{Key: "tls_ca", Type: plugin.TypeString, Multiline: true, Format: "pem", Group: GroupTLS, ShowIf: cond, Label: "plugin.common.tls_ca.label", Help: "plugin.common.tls_ca.help"},
		{Key: "tls_cert", Type: plugin.TypeString, Multiline: true, Format: "pem", Group: GroupTLS, ShowIf: cond, Label: "plugin.common.tls_cert.label"},
		{Key: "tls_key", Type: plugin.TypeString, Multiline: true, Format: "pem", Secret: true, Group: GroupTLS, ShowIf: cond, Label: "plugin.common.tls_key.label"},
	}
}

// ApplyTLSMaterial adds the configured CA and client certificate to cfg. Without a CA the system
// pool is used.
func ApplyTLSMaterial(cfg *tls.Config, values map[string]any) error {
	if cfg == nil {
		return nil
	}
	if ca := Str(values, "tls_ca"); ca != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(ca)) {
			return plugin.NewConnError(plugin.ErrCodeTLSFailed, errors.New("the CA certificate is not valid PEM"))
		}
		cfg.RootCAs = pool
	}
	cert, key := Str(values, "tls_cert"), Str(values, "tls_key")
	if cert != "" || key != "" {
		pair, err := tls.X509KeyPair([]byte(cert), []byte(key))
		if err != nil {
			return plugin.NewConnError(plugin.ErrCodeTLSFailed, errors.New("the client certificate or key is not valid PEM"))
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return nil
}
