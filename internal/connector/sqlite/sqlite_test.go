package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/connectortest"
)

func fixture(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	connectortest.Must(t, err, "open fixture")
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`
		CREATE TABLE rb_types (
			id INTEGER, c_int INTEGER, c_decimal DECIMAL(10,2), c_float REAL, c_text TEXT,
			c_bool BOOLEAN, c_date DATE, c_datetime DATETIME, c_time TIME, c_json JSON, c_blob BLOB);
		INSERT INTO rb_types VALUES (1, 42, 12.34, 1.5, 'olá', 1, '2026-09-25', '2026-09-25 10:11:12',
			'10:11:12', '{"a":1}', x'00ff');
		INSERT INTO rb_types (id) VALUES (2);
		CREATE TABLE rb_rows (n INTEGER);
		WITH RECURSIVE s(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM s WHERE n < 10) INSERT INTO rb_rows SELECT n FROM s;`)
	connectortest.Must(t, err, "create fixture")
}

func TestConformance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.db")
	outside := filepath.Join(t.TempDir(), "outside.db")
	store := filepath.Join(dir, "rowbird.db")
	for _, p := range []string{outside, store} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	connectortest.Run(t, connectortest.Harness{
		Connector: connector{},
		Values:    map[string]any{"path": path},
		Setup:     func(t *testing.T) { fixture(t, path) },
		Types: []connectortest.ExpectedColumn{
			{Name: "c_int", Type: plugin.TypeInt, Value: int64(42)},
			{Name: "c_decimal", Type: plugin.TypeDecimal, Value: plugin.Decimal("12.34")},
			{Name: "c_float", Type: plugin.TypeFloat, Value: 1.5},
			{Name: "c_text", Type: plugin.TypeText, Value: "olá"},
			{Name: "c_bool", Type: plugin.TypeBool, Value: true},
			{Name: "c_date", Type: plugin.TypeDate, Value: plugin.Date("2026-09-25")},
			{Name: "c_datetime", Type: plugin.TypeDateTime, Value: time.Date(2026, 9, 25, 10, 11, 12, 0, time.UTC)},
			{Name: "c_time", Type: plugin.TypeTime, Value: plugin.TimeOfDay("10:11:12")},
			{Name: "c_json", Type: plugin.TypeJSON, Value: plugin.JSON(`{"a":1}`)},
			{Name: "c_blob", Type: plugin.TypeBinary, Value: []byte{0, 255}},
		},
		SlowSQL:   "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT max(x) FROM c",
		ParamSQL:  "SELECT ?",
		InsertSQL: "INSERT INTO rb_rows (n) VALUES (99)",
		Errors: map[string]map[string]any{
			plugin.ErrCodeDatabaseNotFound: {"path": filepath.Join(dir, "missing.db")},
			plugin.ErrCodePathNotAllowed:   {"path": outside},
		},
		Options: plugin.OpenOptions{SQLiteDirs: []string{dir}, ForbiddenPaths: []string{store}},
	})
}

func TestAllowedPath(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	inside := filepath.Join(dir, "a.db")
	outside := filepath.Join(other, "b.db")
	store := filepath.Join(dir, "rowbird.db")
	for _, p := range []string{inside, outside, store} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	escape := filepath.Join(dir, "escape.db")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		path string
		code string
	}{
		"inside":               {inside, ""},
		"relative":             {"a.db", plugin.ErrCodePathNotAllowed},
		"outside":              {outside, plugin.ErrCodePathNotAllowed},
		"dot-dot":              {filepath.Join(dir, "..", filepath.Base(other), "b.db"), plugin.ErrCodePathNotAllowed},
		"symlink escape":       {escape, plugin.ErrCodePathNotAllowed},
		"internal store":       {store, plugin.ErrCodePathNotAllowed},
		"missing":              {filepath.Join(dir, "nope.db"), plugin.ErrCodeDatabaseNotFound},
		"directory":            {filepath.Join(dir, "sub"), plugin.ErrCodeDatabaseNotFound},
		"the directory itself": {dir, plugin.ErrCodePathNotAllowed},
	}
	for name, tc := range cases {
		_, err := allowedPath(tc.path, []string{dir}, []string{store})
		if tc.code == "" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if ce, ok := plugin.AsConnError(err); !ok || ce.Code != tc.code {
			t.Errorf("%s: got %v, want %s", name, err, tc.code)
		}
	}
	if _, err := allowedPath(inside, nil, nil); err == nil {
		t.Error("no allowed directories must allow nothing")
	}
}
