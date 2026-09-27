package netx

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestIsBlocked(t *testing.T) {
	blocked := []string{
		"10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1", "127.0.0.1", "169.254.169.254",
		"100.64.0.1", "0.0.0.0", "224.0.0.1", "::1", "fd00:ec2::254", "fe80::1", "::ffff:10.0.0.1", "::",
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "172.32.0.1", "2001:4860:4860::8888", "::ffff:8.8.8.8"}
	for _, s := range blocked {
		if !IsBlocked(netip.MustParseAddr(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range allowed {
		if IsBlocked(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := f[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

func TestDialContext(t *testing.T) {
	resolver := fakeResolver{
		"db.example":     {netip.MustParseAddr("203.0.113.5")},
		"internal.corp":  {netip.MustParseAddr("10.0.0.5")},
		"mixed.example":  {netip.MustParseAddr("10.0.0.5"), netip.MustParseAddr("203.0.113.9")},
		"rebind.example": {netip.MustParseAddr("127.0.0.1")},
	}
	var dialed []string
	d := &Dialer{Policy: PolicyBlockPrivate, Resolver: resolver, dial: func(_ context.Context, _, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		c1, c2 := net.Pipe()
		_ = c2.Close()
		return c1, nil
	}}

	for _, tc := range []struct {
		addr, want string
		blocked    bool
	}{
		{"db.example:5432", "203.0.113.5:5432", false},
		{"internal.corp:5432", "", true},
		{"mixed.example:3306", "203.0.113.9:3306", false}, // the private answer is skipped
		{"rebind.example:80", "", true},
		{"169.254.169.254:80", "", true},
		{"[::1]:5432", "", true},
		{"8.8.8.8:53", "8.8.8.8:53", false},
	} {
		dialed = nil
		conn, err := d.DialContext(t.Context(), "tcp", tc.addr)
		if tc.blocked {
			if !errors.Is(err, ErrBlocked) {
				t.Errorf("%s: got %v, want ErrBlocked", tc.addr, err)
			}
			if len(dialed) != 0 {
				t.Errorf("%s: dialed %v despite the block", tc.addr, dialed)
			}
			continue
		}
		if err != nil || len(dialed) != 1 || dialed[0] != tc.want {
			t.Errorf("%s: err=%v dialed=%v, want %s", tc.addr, err, dialed, tc.want)
		}
		_ = conn.Close()
	}

	open := &Dialer{Policy: PolicyOpen, Resolver: resolver, dial: d.dial}
	dialed = nil
	if _, err := open.DialContext(t.Context(), "tcp", "internal.corp:5432"); err != nil || dialed[0] != "10.0.0.5:5432" {
		t.Fatalf("open policy: %v %v", err, dialed)
	}
	if _, err := open.DialContext(t.Context(), "tcp", "nope.example:1"); err == nil {
		t.Fatal("resolution failure not reported")
	}
	if _, err := open.DialContext(t.Context(), "tcp", "db.example:99999"); err == nil {
		t.Fatal("bad port accepted")
	}
}
