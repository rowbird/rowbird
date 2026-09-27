// Package sshtunnel reaches a database through an SSH bastion (docs/spec/04-plugins.md, common
// connector options). The bastion's host key must match a confirmed SHA256 fingerprint: an unknown
// key stops the connection and reports the fingerprint so an admin can confirm it, and a changed
// key is always refused.
package sshtunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
)

// Auth methods.
const (
	AuthPassword = "password"
	AuthKey      = "key"
)

// Config describes the bastion.
type Config struct {
	Host       string
	Port       int
	User       string
	Auth       string
	Password   string
	PrivateKey string
	Passphrase string
	// Fingerprint is the confirmed host key, "SHA256:..." as printed by ssh-keygen -lf.
	Fingerprint string
	Timeout     time.Duration
}

// Tunnel is an open SSH client that dials targets from the bastion.
type Tunnel struct {
	client *ssh.Client
}

type hostKeyError struct {
	code        string
	fingerprint string
}

func (e *hostKeyError) Error() string { return e.code + " " + e.fingerprint }

// Open connects to the bastion using dial (the policy dialer) for the TCP connection.
func Open(ctx context.Context, cfg Config, dial plugin.DialFunc) (*Tunnel, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	auth, err := authMethods(cfg)
	if err != nil {
		return nil, plugin.NewConnError(plugin.ErrCodeSSHAuthFailed, err)
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	dialCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	conn, err := dial(dialCtx, "tcp", addr)
	if err != nil {
		if errors.Is(err, netx.ErrBlocked) {
			return nil, plugin.NewConnError(plugin.ErrCodeNetworkBlocked, err)
		}
		return nil, plugin.NewConnError(plugin.ErrCodeSSHUnreachable, err)
	}
	if deadline, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	clientCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auth,
		HostKeyCallback: checkHostKey(cfg.Fingerprint),
		Timeout:         cfg.Timeout,
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, clientCfg)
	if err != nil {
		_ = conn.Close()
		var hk *hostKeyError
		switch {
		case errors.As(err, &hk):
			return nil, &plugin.ConnError{Code: hk.code, Detail: map[string]string{"fingerprint": hk.fingerprint}, Err: err}
		case isAuthError(err):
			return nil, plugin.NewConnError(plugin.ErrCodeSSHAuthFailed, err)
		}
		return nil, plugin.NewConnError(plugin.ErrCodeSSHUnreachable, err)
	}
	_ = conn.SetDeadline(time.Time{})
	return &Tunnel{client: ssh.NewClient(c, chans, reqs)}, nil
}

func checkHostKey(want string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		got := ssh.FingerprintSHA256(key)
		switch {
		case want == "":
			return &hostKeyError{code: plugin.ErrCodeSSHHostKeyUnknown, fingerprint: got}
		case got != want:
			return &hostKeyError{code: plugin.ErrCodeSSHHostKeyChanged, fingerprint: got}
		}
		return nil
	}
}

func authMethods(cfg Config) ([]ssh.AuthMethod, error) {
	switch cfg.Auth {
	case AuthPassword:
		return []ssh.AuthMethod{ssh.Password(cfg.Password)}, nil
	case AuthKey:
		var signer ssh.Signer
		var err error
		if cfg.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(cfg.PrivateKey), []byte(cfg.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(cfg.PrivateKey))
		}
		if err != nil {
			return nil, fmt.Errorf("sshtunnel: private key cannot be read (wrong passphrase or format)")
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	return nil, fmt.Errorf("sshtunnel: unknown auth method %q", cfg.Auth)
}

func isAuthError(err error) bool {
	var e *ssh.ServerAuthError
	if errors.As(err, &e) {
		return true
	}
	// x/crypto/ssh reports exhausted methods as a plain error.
	return err != nil && containsAny(err.Error(), "unable to authenticate", "no supported methods remain")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// Dial opens a TCP connection from the bastion to addr. The address is resolved by the bastion,
// so the local network policy does not apply to it.
func (t *Tunnel) Dial(ctx context.Context, _ string, addr string) (net.Conn, error) {
	return t.client.DialContext(ctx, "tcp", addr)
}

// Close closes the SSH connection and every forwarded connection.
func (t *Tunnel) Close() error { return t.client.Close() }
