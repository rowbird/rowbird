//go:build integration

package email

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rowbird/rowbird/internal/plugin/destinationtest"
)

// TestMailpit delivers a real message to Mailpit and reads it back through its API.
func TestMailpit(t *testing.T) {
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "axllent/mailpit:v1.21", ExposedPorts: []string{"1025/tcp", "8025/tcp"},
			WaitingFor: wait.ForHTTP("/livez").WithPort("8025/tcp"),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start mailpit: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	host, _ := c.Host(ctx)
	smtp, _ := c.MappedPort(ctx, "1025/tcp")
	api, _ := c.MappedPort(ctx, "8025/tcp")
	port, _ := strconv.Atoi(smtp.Port())

	h := destinationtest.Harness{
		Destination: dest{},
		Config:      map[string]any{"host": host, "port": int64(port), "tls_mode": "none", "from_address": "rowbird@example.com", "from_name": "Rowbird"},
		Options:     map[string]any{"to": "ana@example.com"},
	}
	msg := destinationtest.Message("pt-BR")
	msg.Inline = "<table><tr><td>Sul</td></tr></table>"
	if _, err := (dest{}).Send(ctx, h.Env(t), msg); err != nil {
		t.Fatal(err)
	}
	res, err := http.Get("http://" + host + ":" + api.Port() + "/api/v1/messages") //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var list struct {
		Messages []struct {
			Subject     string `json:"Subject"`
			Attachments int    `json:"Attachments"`
			To          []struct {
				Address string `json:"Address"`
			} `json:"To"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Messages) != 1 || list.Messages[0].Subject != `Vendas por região <Q3> & "total" (2026-09-25)` ||
		list.Messages[0].Attachments != 1 || list.Messages[0].To[0].Address != "ana@example.com" {
		t.Fatalf("mailpit received %+v", list)
	}
}
