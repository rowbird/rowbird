package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/api/gen"
	_ "github.com/rowbird/rowbird/internal/destination/webhook"
)

func TestChannelsOverHTTP(t *testing.T) {
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer hook.Close()
	ts := newTestServer(t)
	admin := ts.setup()
	const secret = "whsec-http-secret"

	var ch gen.Channel
	res := admin.do(http.MethodPost, "/api/v1/channels", map[string]any{"name": "ops-hook", "type": "webhook", "config": map[string]any{"url": hook.URL, "hmac_secret": secret}})
	res.decode(t, &ch)
	if res.Code != 201 || strings.Contains(res.Body.String(), secret) || ch.Config["hmac_secret"].(map[string]any)["configured"] != true || ch.Status != gen.ChannelStatusUnknown {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	var result gen.ChannelTestResult
	res = admin.do(http.MethodPost, "/api/v1/channels/"+ch.Id.String()+"/test", nil)
	res.decode(t, &result)
	if res.Code != 200 || !result.Ok {
		t.Fatalf("test: %d %s", res.Code, res.Body)
	}
	admin.do(http.MethodGet, "/api/v1/channels/"+ch.Id.String(), nil).decode(t, &ch)
	if ch.Status != gen.ChannelStatusOk || ch.LastSuccessAt == nil {
		t.Fatalf("health %+v", ch)
	}
	res = admin.do(http.MethodPost, "/api/v1/channels/test", map[string]any{"type": "webhook", "config": map[string]any{"url": "http://127.0.0.1:1/"}})
	res.decode(t, &result)
	if result.Ok || result.ErrorCode == nil || *result.ErrorCode != "delivery.unreachable" {
		t.Fatalf("unsaved test: %s", res.Body)
	}
	if p := admin.do(http.MethodPost, "/api/v1/channels", map[string]any{"name": "x", "type": "webhook", "config": map[string]any{"url": "javascript:alert(1)"}}).problem(t); p.Errors == nil || (*p.Errors)[0].Field != "config.url" {
		t.Fatalf("invalid url: %+v", p)
	}
	if res := admin.do(http.MethodDelete, "/api/v1/channels/"+ch.Id.String(), nil); res.Code != 204 {
		t.Fatalf("delete: %d", res.Code)
	}
}
