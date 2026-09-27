package gitops_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/rowbird/rowbird/internal/format/golden"
	"github.com/rowbird/rowbird/internal/gitops"
)

func TestExportWorkspace(t *testing.T) {
	w := newWorkspace(t)
	w.seed(t)
	docs, err := w.exporter.Export(w.Ctx, gitops.Selection{IncludeConnections: true, IncludeChannels: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := gitops.Encode(docs)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(out), w.Dir, "/data/sqlite")
	if strings.Contains(text, "hook-secret-value") {
		t.Fatal("a secret was exported")
	}
	golden.Check(t, "testdata/export.golden.yaml", []byte(text))

	// The export reads back as the same documents.
	back, err := gitops.ParseSet("export.yaml", out)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := gitops.Encode(back)
	if string(again) != string(out) {
		t.Errorf("export does not read back the same:\n%s", again)
	}
}

func TestExportSelection(t *testing.T) {
	w := newWorkspace(t)
	w.seed(t)
	w.Query(t, "unrelated", "select 1")
	docs, err := w.exporter.Export(w.Ctx, gitops.Selection{Reports: []string{"daily-orders"}})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, d := range docs {
		keys = append(keys, d.Key())
	}
	if strings.Join(keys, ",") != "Query/orders-by-region,Report/daily-orders" {
		t.Errorf("selection exported %v", keys)
	}
	docs, _ = w.exporter.Export(w.Ctx, gitops.Selection{Reports: []string{"daily-orders"}, IncludeConnections: true, IncludeChannels: true})
	if len(docs) != 4 {
		t.Errorf("with dependencies: %d documents", len(docs))
	}
	var unknown *gitops.ErrUnknownSelection
	if _, err := w.exporter.Export(w.Ctx, gitops.Selection{Reports: []string{"nope"}}); !errors.As(err, &unknown) {
		t.Errorf("unknown report: %v", err)
	}
}
