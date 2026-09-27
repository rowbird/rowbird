package sshtunnel

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/rowbird/rowbird/internal/connector/sshtunnel/sshtest"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
)

var openDial = netx.NewDialer(netx.PolicyOpen).DialContext

// wantCode checks the error code and returns the error's safe details.
func wantCode(t *testing.T, err error, code string) map[string]string {
	t.Helper()
	ce, ok := plugin.AsConnError(err)
	if !ok || ce.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
	return ce.Detail
}

func echoThrough(t *testing.T, tun *Tunnel, target string) {
	t.Helper()
	c, err := tun.Dial(t.Context(), "tcp", target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || line != "ping\n" {
		t.Fatalf("echo through tunnel: %q %v", line, err)
	}
}

func TestPasswordTunnelAndHostKeyConfirmation(t *testing.T) {
	srv := sshtest.Start(t, "bastion", "s3cret", nil)
	target := sshtest.Echo(t)
	cfg := Config{Host: "127.0.0.1", Port: srv.Port, User: "bastion", Auth: AuthPassword, Password: "s3cret"}

	// First contact: the fingerprint is unknown and reported for confirmation.
	_, err := Open(t.Context(), cfg, openDial)
	detail := wantCode(t, err, plugin.ErrCodeSSHHostKeyUnknown)
	if detail["fingerprint"] != srv.Fingerprint || !strings.HasPrefix(srv.Fingerprint, "SHA256:") {
		t.Fatalf("fingerprint %q, server %q", detail["fingerprint"], srv.Fingerprint)
	}

	cfg.Fingerprint = srv.Fingerprint
	tun, err := Open(t.Context(), cfg, openDial)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tun.Close() }()
	echoThrough(t, tun, target)
	if got := srv.Forwarded(); len(got) != 1 || got[0] != target {
		t.Fatalf("forwarded %v", got)
	}

	cfg.Fingerprint = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	_, err = Open(t.Context(), cfg, openDial)
	detail = wantCode(t, err, plugin.ErrCodeSSHHostKeyChanged)
	if detail["fingerprint"] != srv.Fingerprint {
		t.Fatal("mismatch does not report the presented key")
	}
}

func TestKeyAuth(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	srv := sshtest.Start(t, "bastion", "unused", sshPub)
	target := sshtest.Echo(t)

	for _, passphrase := range []string{"", "open sesame"} {
		var block *pem.Block
		var err error
		if passphrase == "" {
			block, err = ssh.MarshalPrivateKey(priv, "")
		} else {
			block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
		}
		if err != nil {
			t.Fatal(err)
		}
		cfg := Config{
			Host: "127.0.0.1", Port: srv.Port, User: "bastion", Auth: AuthKey,
			PrivateKey: string(pem.EncodeToMemory(block)), Passphrase: passphrase, Fingerprint: srv.Fingerprint,
		}
		tun, err := Open(t.Context(), cfg, openDial)
		if err != nil {
			t.Fatalf("passphrase %q: %v", passphrase, err)
		}
		echoThrough(t, tun, target)
		_ = tun.Close()

		if passphrase != "" {
			cfg.Passphrase = "wrong"
			_, err := Open(t.Context(), cfg, openDial)
			wantCode(t, err, plugin.ErrCodeSSHAuthFailed)
		}
	}
}

func TestFailures(t *testing.T) {
	srv := sshtest.Start(t, "bastion", "s3cret", nil)
	base := Config{Host: "127.0.0.1", Port: srv.Port, User: "bastion", Auth: AuthPassword, Password: "wrong", Fingerprint: srv.Fingerprint}

	_, err := Open(t.Context(), base, openDial)
	wantCode(t, err, plugin.ErrCodeSSHAuthFailed)

	ln, _ := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	closed := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	unreachable := base
	unreachable.Port = closed
	_, err = Open(t.Context(), unreachable, openDial)
	wantCode(t, err, plugin.ErrCodeSSHUnreachable)

	_, err = Open(t.Context(), base, netx.NewDialer(netx.PolicyBlockPrivate).DialContext)
	wantCode(t, err, plugin.ErrCodeNetworkBlocked)

	badKey := base
	badKey.Auth, badKey.PrivateKey = AuthKey, "not a key"
	_, err = Open(t.Context(), badKey, openDial)
	wantCode(t, err, plugin.ErrCodeSSHAuthFailed)
	if strings.Contains(err.Error(), "not a key") {
		t.Fatal("error echoes the private key")
	}
}
