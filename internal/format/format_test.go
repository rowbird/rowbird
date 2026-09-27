package format

import (
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/plugin"
)

func TestNumber(t *testing.T) {
	cases := []struct {
		in, en, pt, ptPlain string
	}{
		{"0", "0", "0", "0"},
		{"1234.56", "1,234.56", "1.234,56", "1234,56"},
		{"-1234567", "-1,234,567", "-1.234.567", "-1234567"},
		{"123", "123", "123", "123"},
		{"+1000", "1,000", "1.000", "1000"},
		{".5", "0.5", "0,5", "0,5"},
		{
			"12345678901234567890123456789012345678.25", "12,345,678,901,234,567,890,123,456,789,012,345,678.25",
			"12.345.678.901.234.567.890.123.456.789.012.345.678,25", "12345678901234567890123456789012345678,25",
		},
		{"1e10", "1e10", "1e10", "1e10"},
		{"NaN", "NaN", "NaN", "NaN"},
		{"1.", "1.", "1.", "1."},
	}
	en, pt := GetLocale("en"), GetLocale("pt-BR")
	for _, tc := range cases {
		if got := en.Number(tc.in, true); got != tc.en {
			t.Errorf("en %q = %q, want %q", tc.in, got, tc.en)
		}
		if got := pt.Number(tc.in, true); got != tc.pt {
			t.Errorf("pt-BR %q = %q, want %q", tc.in, got, tc.pt)
		}
		if got := pt.Number(tc.in, false); got != tc.ptPlain {
			t.Errorf("pt-BR plain %q = %q, want %q", tc.in, got, tc.ptPlain)
		}
	}
	if GetLocale("fr").Tag != "en" {
		t.Error("unknown locales fall back to English")
	}
}

func TestText(t *testing.T) {
	sp, _ := time.LoadLocation("America/Sao_Paulo")
	tz := plugin.Column{Name: "at", Type: plugin.TypeDateTime, WithTimeZone: true}
	naive := plugin.Column{Name: "at", Type: plugin.TypeDateTime}
	col := plugin.Column{Name: "x"}
	at := time.Date(2026, 9, 25, 13, 30, 0, 0, time.UTC)
	cases := []struct {
		col    plugin.Column
		v      any
		en, pt string
	}{
		{col, nil, "", ""},
		{col, int64(-1234), "-1,234", "-1.234"},
		{col, 1234.5, "1,234.5", "1.234,5"},
		{col, plugin.Decimal("9876.10"), "9,876.10", "9.876,10"},
		{col, true, "yes", "sim"},
		{col, false, "no", "não"},
		{col, plugin.Date("2026-09-25"), "2026-09-25", "25/09/2026"},
		{tz, at, "2026-09-25 10:30", "25/09/2026 10:30"},
		{naive, at, "2026-09-25 13:30", "25/09/2026 13:30"},
		{naive, at.Add(5 * time.Second), "2026-09-25 13:30:05", "25/09/2026 13:30:05"},
		{col, []byte{1, 2, 3}, "(3 bytes)", "(3 bytes)"},
		{col, plugin.TimeOfDay("08:30:00"), "08:30:00", "08:30:00"},
		{col, plugin.JSON(`{"a":1}`), `{"a":1}`, `{"a":1}`},
		{col, "olá", "olá", "olá"},
	}
	for _, tc := range cases {
		if got := Text(tc.col, tc.v, "en", sp); got != tc.en {
			t.Errorf("en %#v = %q, want %q", tc.v, got, tc.en)
		}
		if got := Text(tc.col, tc.v, "pt-BR", sp); got != tc.pt {
			t.Errorf("pt-BR %#v = %q, want %q", tc.v, got, tc.pt)
		}
	}
}

func TestNeutralize(t *testing.T) {
	cases := map[string]string{
		"=SUM(A1)": "'=SUM(A1)", "+1": "'+1", "-2": "'-2", "@x": "'@x", "\tx": "'\tx", "\rx": "'\rx",
		"": "", "ok": "ok", " =x": " =x",
	}
	for in, want := range cases {
		if got := Neutralize(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

func TestT(t *testing.T) {
	if got := T("pt-BR", "format.showing", map[string]any{"shown": 20, "total": 150}); got != "Mostrando 20 de 150 linhas." {
		t.Errorf("got %q", got)
	}
	if got := T("fr", "format.noRows", nil); got != "No rows." {
		t.Errorf("fallback %q", got)
	}
	if got := T("en", "format.nope", nil); got != "format.nope" {
		t.Errorf("unknown key %q", got)
	}
}

func TestOptionsAndRows(t *testing.T) {
	opts := map[string]any{"s": "x", "b": true, "empty": ""}
	if String(opts, "s", "d") != "x" || String(opts, "empty", "d") != "d" || String(opts, "none", "d") != "d" {
		t.Error("String")
	}
	if !Bool(opts, "b", false) || Bool(opts, "none", false) {
		t.Error("Bool")
	}
	rows := SliceRows([][]any{{int64(1)}, {int64(2)}})
	n := 0
	for rows.Next() {
		n += int(rows.Row()[0].(int64))
	}
	if n != 3 || rows.Err() != nil {
		t.Errorf("rows %d", n)
	}
}
