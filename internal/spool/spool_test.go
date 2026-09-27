package spool

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/plugin"
)

var cols = []plugin.Column{{Name: "a", Type: plugin.TypeInt}, {Name: "b", Type: plugin.TypeText}}

func write(t *testing.T, path string, rows [][]any) *Writer {
	t.Helper()
	w, err := Create(path, cols)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if err := w.Write(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 25, 10, 30, 0, 123456000, time.FixedZone("x", -3*3600))
	rows := [][]any{
		{
			int64(-42), "olá", 1.5, true, false,
			[]byte{0, 1},
			at, plugin.Decimal("12345678901234567890.25"),
			plugin.Date("2026-09-25"), plugin.TimeOfDay("08:30:00"), plugin.JSON(`{"a":1}`), nil,
		},
		{},
		{int64(1)},
	}
	path := filepath.Join(t.TempDir(), "r.spool")
	w, err := Create(path, append(cols, make([]plugin.Column, 10)...))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if err := w.Write(r); err != nil {
			t.Fatal(err)
		}
	}
	_ = w.Close()
	if w.Rows() != 3 || len(w.Hash()) != 64 {
		t.Fatalf("rows %d hash %q", w.Rows(), w.Hash())
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if len(r.Columns()) != 12 || r.Columns()[0].Name != "a" {
		t.Fatalf("columns %+v", r.Columns())
	}
	var got [][]any
	for r.Next() {
		got = append(got, r.Row())
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
	if !got[0][6].(time.Time).Equal(at) {
		t.Errorf("time %v", got[0][6])
	}
	got[0][6], rows[0][6] = nil, nil
	if !reflect.DeepEqual(got, rows) {
		t.Errorf("rows\n%#v\nwant\n%#v", got, rows)
	}
}

func TestHash(t *testing.T) {
	dir := t.TempDir()
	a := write(t, filepath.Join(dir, "a"), [][]any{{int64(1), "x"}, {int64(2), "y"}})
	b := write(t, filepath.Join(dir, "b"), [][]any{{int64(1), "x"}, {int64(2), "y"}})
	c := write(t, filepath.Join(dir, "c"), [][]any{{int64(2), "y"}, {int64(1), "x"}})
	d := write(t, filepath.Join(dir, "d"), [][]any{{int64(1), "x"}, {int64(2), nil}})
	if a.Hash() != b.Hash() || a.Hash() == c.Hash() || a.Hash() == d.Hash() {
		t.Errorf("hashes %s %s %s %s", a.Hash(), b.Hash(), c.Hash(), d.Hash())
	}
	empty := write(t, filepath.Join(dir, "e"), nil)
	if empty.Hash() == a.Hash() || empty.Rows() != 0 {
		t.Error("empty result hash")
	}
}

func TestErrors(t *testing.T) {
	dir := t.TempDir()
	w, _ := Create(filepath.Join(dir, "x"), cols)
	if err := w.Write([]any{struct{}{}}); err == nil {
		t.Error("unsupported type accepted")
	}
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x")); !errors.Is(err, os.ErrNotExist) {
		t.Error("abort left the file")
	}
	_ = os.WriteFile(filepath.Join(dir, "bad"), []byte("nope"), 0o600)
	if _, err := Open(filepath.Join(dir, "bad")); !errors.Is(err, ErrFormat) {
		t.Errorf("bad file: %v", err)
	}
	write(t, filepath.Join(dir, "dup"), nil)
	if _, err := Create(filepath.Join(dir, "dup"), cols); err == nil {
		t.Error("overwrote an existing spool")
	}
}
