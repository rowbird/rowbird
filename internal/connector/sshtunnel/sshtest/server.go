// Package sshtest runs an in-process SSH server that forwards direct-tcpip channels, for testing
// tunnels without a container.
package sshtest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// Server is a running test SSH server.
type Server struct {
	Addr        string
	Port        int
	Fingerprint string
	User        string
	Password    string
	// Forwarded records the target addresses the server was asked to reach.
	mu        sync.Mutex
	forwarded []string
}

// Forwarded returns the targets reached through the server.
func (s *Server) Forwarded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.forwarded...)
}

// Start listens on 127.0.0.1 and accepts password auth for user/password and public key auth for
// authorizedKey (may be nil). It stops when the test ends.
func Start(t *testing.T, user, password string, authorizedKey ssh.PublicKey) *Server {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == user && string(pw) == password {
				return nil, nil
			}
			return nil, errDenied
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if authorizedKey != nil && c.User() == user && string(key.Marshal()) == string(authorizedKey.Marshal()) {
				return nil, nil
			}
			return nil, errDenied
		},
	}
	cfg.AddHostKey(hostSigner)

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &Server{
		Addr: ln.Addr().String(), Port: ln.Addr().(*net.TCPAddr).Port,
		Fingerprint: ssh.FingerprintSHA256(hostSigner.PublicKey()), User: user, Password: password,
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn, cfg)
		}
	}()
	return s
}

type deniedError struct{}

func (deniedError) Error() string { return "denied" }

var errDenied = deniedError{}

func (s *Server) serve(conn net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		_ = conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "direct-tcpip" {
			_ = nc.Reject(ssh.UnknownChannelType, "only direct-tcpip")
			continue
		}
		target, ok := parseDirectTCPIP(nc.ExtraData())
		if !ok {
			_ = nc.Reject(ssh.ConnectionFailed, "bad payload")
			continue
		}
		s.mu.Lock()
		s.forwarded = append(s.forwarded, target)
		s.mu.Unlock()
		upstream, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", target)
		if err != nil {
			_ = nc.Reject(ssh.ConnectionFailed, err.Error())
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			_ = upstream.Close()
			continue
		}
		go ssh.DiscardRequests(chReqs)
		go func() {
			_, _ = io.Copy(ch, upstream)
			_ = ch.CloseWrite()
		}()
		go func() {
			_, _ = io.Copy(upstream, ch)
			_ = upstream.Close()
		}()
	}
}

// parseDirectTCPIP decodes RFC 4254 section 7.2: host string, port uint32, originator, port.
func parseDirectTCPIP(b []byte) (string, bool) {
	if len(b) < 4 {
		return "", false
	}
	n := binary.BigEndian.Uint32(b)
	if len(b) < int(4+n+4) {
		return "", false
	}
	host := string(b[4 : 4+n])
	port := binary.BigEndian.Uint32(b[4+n:])
	return net.JoinHostPort(host, strconv.Itoa(int(port))), true
}

// Echo starts a TCP server that echoes what it reads; it returns its address.
func Echo(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = io.Copy(c, c)
				_ = c.Close()
			}()
		}
	}()
	return ln.Addr().String()
}
