package netx

import (
	"context"
	"net"
	"net/http"
	"time"
)

// DialFunc dials a network address; Dialer.DialContext is one.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// HTTPClient returns a client whose connections go through dial, so that the network policy
// applies to every request (destinations, AI providers). Redirects are followed at most three
// times, and each hop is dialed through the policy again.
func HTTPClient(dial DialFunc, timeout time.Duration) *http.Client {
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dial,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Transport: t,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}
