package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/plugin/destinationtest"
)

const secret = "whsec-0123456789"

type fake struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
	fail     int
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != 0 {
		w.WriteHeader(f.fail)
		_, _ = w.Write([]byte("bad token " + r.Header.Get("Authorization")))
		return
	}
	f.requests = append(f.requests, r)
	f.bodies = append(f.bodies, body)
}

func (f *fake) sent() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.requests) }

func TestConformance(t *testing.T) {
	f := &fake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	destinationtest.Run(t, destinationtest.Harness{
		Destination: dest{},
		Config:      map[string]any{"url": srv.URL + "/hook", "headers": "X-Team: data\nbad line", "secret_headers": "Authorization: Bearer tok-secret-value", "hmac_secret": secret},
		Options:     map[string]any{"include_rows": 5},
		Sent:        f.sent,
		Fail:        func(s int) { f.mu.Lock(); f.fail = s; f.mu.Unlock() },
	})
}

func TestPayloadAndSignature(t *testing.T) {
	f := &fake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	fixed := time.Unix(1790000000, 0)
	Now = func() time.Time { return fixed }
	defer func() { Now = time.Now }()
	h := destinationtest.Harness{Destination: dest{}, Config: map[string]any{"url": srv.URL, "hmac_secret": secret, "secret_headers": "Authorization: Bearer x"}, Options: map[string]any{"include_rows": 1, "include_links": false}}
	msg := destinationtest.Message("en")
	if _, err := (dest{}).Send(context.Background(), h.Env(t), msg); err != nil {
		t.Fatal(err)
	}
	r, body := f.requests[0], f.bodies[0]
	if r.Header.Get("X-Rowbird-Event") != EventRun || r.Header.Get("X-Rowbird-Delivery") != msg.DeliveryID || r.Header.Get("Authorization") != "Bearer x" {
		t.Errorf("headers %v", r.Header)
	}
	sig := r.Header.Get("X-Rowbird-Signature")
	if !Verify(secret, sig, body, fixed.Add(time.Minute), 5*time.Minute) {
		t.Errorf("signature %q does not verify", sig)
	}
	if Verify(secret, sig, body, fixed.Add(6*time.Minute), 5*time.Minute) || Verify("other", sig, body, fixed, 5*time.Minute) || Verify(secret, sig, append(body, ' '), fixed, 5*time.Minute) {
		t.Error("stale, wrong-secret or altered requests verified")
	}
	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	if p.Report.Slug != "vendas-por-regiao" || p.Run.Rows != 1234 || len(p.Rows) != 1 || p.Rows[0]["regiao"] != "Sul" || p.Links != nil || p.Status != "up" {
		t.Errorf("payload %+v", p)
	}
}

func TestParseHeaders(t *testing.T) {
	got := ParseHeaders("A: 1\nX-Rowbird-Event: spoof\nHost: evil\nbad name: x\n: empty\nB:2")
	if len(got) != 2 || got["A"] != "1" || got["B"] != "2" {
		t.Errorf("headers %v", got)
	}
}
