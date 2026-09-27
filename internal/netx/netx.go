// Package netx provides the outbound dialer shared by connectors, destinations and AI providers. It
// enforces ROWBIRD_NETWORK_POLICY (docs/spec/07-security.md, "Network (SSRF)"): the host is resolved
// once, every resolved address is checked, and the connection goes to the checked address, so a DNS
// answer that changes between check and dial cannot reach a blocked network.
package netx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"
)

// Policy values.
const (
	PolicyOpen         = "open"
	PolicyBlockPrivate = "block-private"
)

// ErrBlocked means the destination is not allowed by the network policy.
var ErrBlocked = errors.New("netx: destination blocked by network policy")

// blockedPrefixes cover private, loopback, link-local (which includes cloud metadata at
// 169.254.169.254), shared, reserved and unique-local ranges.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "240.0.0.0/4", "255.255.255.255/32",
		"::/128", "::1/128", "fc00::/7", "fe80::/10", "64:ff9b:1::/48",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// IsBlocked reports whether block-private forbids addr.
func IsBlocked(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.IsMulticast() || addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// Resolver resolves host names; *net.Resolver satisfies it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Dialer dials TCP connections under a policy.
type Dialer struct {
	Policy   string
	Resolver Resolver
	Timeout  time.Duration
	// dial is replaceable in tests.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewDialer returns a dialer for policy with the system resolver.
func NewDialer(policy string) *Dialer {
	return &Dialer{Policy: policy, Resolver: net.DefaultResolver, Timeout: 15 * time.Second}
}

// DialContext resolves, checks and dials addr ("host:port").
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("netx: %w", err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("netx: invalid port %q", portStr)
	}
	addrs, err := d.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	allowed := addrs[:0:0]
	for _, a := range addrs {
		if d.Policy != PolicyBlockPrivate || !IsBlocked(a) {
			allowed = append(allowed, a)
		}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrBlocked, host)
	}

	dial := d.dial
	if dial == nil {
		nd := &net.Dialer{Timeout: d.Timeout, KeepAlive: 30 * time.Second}
		dial = nd.DialContext
	}
	var lastErr error
	for _, a := range allowed {
		conn, err := dial(ctx, network, netip.AddrPortFrom(a, uint16(port)).String())
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (d *Dialer) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a.Unmap()}, nil
	}
	r := d.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	addrs, err := r.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("netx: resolve %s: %w", host, err)
	}
	for i := range addrs {
		addrs[i] = addrs[i].Unmap()
	}
	return addrs, nil
}
