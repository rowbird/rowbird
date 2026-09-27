package uptimekuma

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/destinationtest"
)

type fake struct {
	mu    sync.Mutex
	calls []url.Values
	fail  int
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != 0 {
		w.WriteHeader(f.fail)
		_, _ = w.Write([]byte(`{"ok":false,"msg":"` + r.URL.Path + `"}`))
		return
	}
	f.calls = append(f.calls, r.URL.Query())
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func TestConformance(t *testing.T) {
	f := &fake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	destinationtest.Run(t, destinationtest.Harness{
		Destination: dest{}, Config: map[string]any{"push_url": srv.URL + "/api/push/tok3n-secret?status=up&msg=OK&ping="},
		Sent: func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) },
		Fail: func(s int) { f.mu.Lock(); f.fail = s; f.mu.Unlock() },
	})
}

func TestStatusMapping(t *testing.T) {
	f := &fake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	h := destinationtest.Harness{Destination: dest{}, Config: map[string]any{"push_url": srv.URL + "/api/push/x"}, Options: map[string]any{"skipped_status": "down", "message": "{{report.slug}} {{run.status}}"}}
	for _, status := range []string{plugin.StatusUp, plugin.StatusDown, plugin.StatusSkipped} {
		msg := destinationtest.Message("pt-BR")
		msg.Status = status
		if _, err := (dest{}).Send(context.Background(), h.Env(t), msg); err != nil {
			t.Fatal(err)
		}
	}
	want := [][2]string{{"up", "vendas-por-regiao concluída"}, {"down", "vendas-por-regiao falhou"}, {"down", "vendas-por-regiao pulada"}}
	for i, w := range want {
		if f.calls[i].Get("status") != w[0] || f.calls[i].Get("msg") != w[1] || f.calls[i].Get("ping") != "1500" {
			t.Errorf("call %d: %v", i, f.calls[i])
		}
	}
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
	// A 200 with ok=false (unknown monitor) is a rejection.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"msg":"Monitor not found"}`))
	}))
	defer bad.Close()
	h.Config = map[string]any{"push_url": bad.URL}
	var de *plugin.DeliveryError
	if _, err := (dest{}).Send(context.Background(), h.Env(t), destinationtest.Message("en")); err == nil || !asDelivery(err, &de) || de.Code != plugin.ErrCodeDeliveryRejected {
		t.Errorf("ok=false: %v", err)
	}
}

func asDelivery(err error, de **plugin.DeliveryError) bool { return errors.As(err, de) }
