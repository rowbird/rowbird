package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type (
	clientIPKey struct{}
	httpsKey    struct{}
)

// ClientIPFrom returns the client address resolved by the clientIP middleware.
func ClientIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey{}).(string)
	return ip
}

// RequestIsHTTPS reports whether the client reached Rowbird over HTTPS: the connection itself is TLS,
// or a trusted proxy said so in X-Forwarded-Proto.
func RequestIsHTTPS(ctx context.Context) bool {
	v, _ := ctx.Value(httpsKey{}).(bool)
	return v
}

// clientIP resolves the real client address. X-Forwarded-For is only honored when the direct peer
// is a trusted proxy (ROWBIRD_TRUSTED_PROXIES); it is then read from the right, skipping trusted
// hops, so a client cannot spoof its address by sending the header itself. X-Forwarded-Proto is honored under
// the same condition, reading its last value (the one the nearest proxy wrote).
func clientIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := peerAddr(r.RemoteAddr)
			https := r.TLS != nil
			if ip.IsValid() && isTrusted(ip) {
				if protos := strings.Split(strings.Join(r.Header.Values("X-Forwarded-Proto"), ","), ","); len(protos) > 0 {
					if p := strings.ToLower(strings.TrimSpace(protos[len(protos)-1])); p != "" {
						https = p == "https"
					}
				}
				hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
				for i := len(hops) - 1; i >= 0; i-- {
					hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
					if err != nil {
						break
					}
					ip = hop.Unmap()
					if !isTrusted(ip) {
						break
					}
				}
			}
			value := ""
			if ip.IsValid() {
				value = ip.String()
			}
			ctx := context.WithValue(r.Context(), clientIPKey{}, value)
			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, httpsKey{}, https)))
		})
	}
}

func peerAddr(remote string) netip.Addr {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}
