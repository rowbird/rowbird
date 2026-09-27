package email

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/destinationtest"
)

// smtpd is a tiny SMTP server: it accepts every message, or rejects the sender with reject.
type smtpd struct {
	ln     net.Listener
	mu     sync.Mutex
	mails  []string
	rcpts  [][]string
	reject string
}

func newSMTPD(t *testing.T) *smtpd {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpd{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *smtpd) port() int64 { return int64(s.ln.Addr().(*net.TCPAddr).Port) }

func (s *smtpd) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReader(c)
	say := func(line string) { _, _ = c.Write([]byte(line + "\r\n")) }
	say("220 fake ESMTP")
	var rcpts []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250-fake")
			say("250 8BITMIME")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			s.mu.Lock()
			reject := s.reject
			s.mu.Unlock()
			if reject != "" {
				say(reject)
				continue
			}
			rcpts = nil
			say("250 OK")
		case strings.HasPrefix(cmd, "RCPT TO"):
			rcpts = append(rcpts, strings.Trim(strings.TrimSpace(line[8:]), "<>"))
			say("250 OK")
		case cmd == "DATA":
			say("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.mu.Lock()
			s.mails = append(s.mails, b.String())
			s.rcpts = append(s.rcpts, rcpts)
			s.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		case cmd == "RSET", cmd == "NOOP":
			say("250 OK")
		default:
			say("502 not implemented")
		}
	}
}

func (s *smtpd) sent() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.mails) }

func harness(s *smtpd) destinationtest.Harness {
	return destinationtest.Harness{
		Destination: dest{},
		Config:      map[string]any{"host": "127.0.0.1", "port": s.port(), "tls_mode": "none", "from_address": "rowbird@example.com"},
		Options:     map[string]any{"to": "ana@example.com, bruno@example.com", "bcc": "audit@example.com"},
		Inline:      `<table><tr><td>Sul</td></tr></table>`,
		Sent:        s.sent,
	}
}

func TestConformance(t *testing.T) {
	destinationtest.Run(t, harness(newSMTPD(t)))
}

func TestMessage(t *testing.T) {
	s := newSMTPD(t)
	h := harness(s)
	msg := destinationtest.Message("pt-BR")
	msg.Inline = h.Inline
	if _, err := (dest{}).Send(context.Background(), h.Env(t), msg); err != nil {
		t.Fatal(err)
	}
	mail := s.mails[0]
	for _, want := range []string{
		"Subject: =?UTF-8?q?Vendas_por_regi=C3=A3o_<Q3>_&_\"total\"_(2026-09-25)?=",
		"To: <ana@example.com>, <bruno@example.com>", "text/plain", "text/html",
		`filename="vendas-por-regiao-2026-09-25.csv"`, "X-Mailer: Rowbird",
	} {
		if !strings.Contains(mail, want) {
			t.Errorf("mail lacks %q", want)
		}
	}
	if strings.Contains(mail, "Bcc:") || len(s.rcpts[0]) != 3 {
		t.Errorf("bcc leaked or missing: %v", s.rcpts[0])
	}
	p, _ := (dest{}).Preview(context.Background(), h.Env(t), msg)
	for _, want := range []string{"Vendas por região &lt;Q3&gt; &amp; &#34;total&#34;", "1.234 linhas", `<table><tr><td>Sul</td></tr></table>`, `href="https://rb.example.com/r/rbl_abc"`, "Enviado pelo Rowbird"} {
		if !strings.Contains(p.Body, want) {
			t.Errorf("html lacks %q", want)
		}
	}
}

func TestFailures(t *testing.T) {
	s := newSMTPD(t)
	h := harness(s)
	for reply, want := range map[string]struct {
		code  string
		retry bool
	}{
		"535 5.7.8 authentication failed": {plugin.ErrCodeDeliveryAuth, false},
		"450 4.2.0 try later":             {plugin.ErrCodeDeliveryFailed, true},
		"550 5.1.0 sender rejected":       {plugin.ErrCodeDeliveryRejected, false},
	} {
		s.mu.Lock()
		s.reject = reply
		s.mu.Unlock()
		_, err := (dest{}).Send(context.Background(), h.Env(t), destinationtest.Message("en"))
		var de *plugin.DeliveryError
		if !errors.As(err, &de) || de.Code != want.code || de.Retry != want.retry {
			t.Errorf("%s: %v", reply, err)
		}
	}
	h.Config["port"] = int64(1)
	_, err := (dest{}).Send(context.Background(), h.Env(t), destinationtest.Message("en"))
	var de *plugin.DeliveryError
	if !errors.As(err, &de) || de.Code != plugin.ErrCodeDeliveryUnreachable || !de.Retry {
		t.Errorf("closed port: %v", err)
	}
}

func TestRecipients(t *testing.T) {
	if got, bad := Recipients("a@example.com; B <b@example.com>,c@example.com"); bad != "" || len(got) != 3 || got[1] != "b@example.com" {
		t.Errorf("%v %q", got, bad)
	}
	if _, bad := Recipients("ok@example.com, nope"); bad != "nope" {
		t.Errorf("bad %q", bad)
	}
	if field, ok := ValidateOptions(map[string]any{"to": "a@example.com", "cc": "x"}); ok || field != "cc" {
		t.Errorf("validate %s %v", field, ok)
	}
}
