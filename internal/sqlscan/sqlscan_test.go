package sqlscan

import (
	"strings"
	"testing"
)

func TestTokensRebuildInput(t *testing.T) {
	inputs := []string{
		"select 'a;b' as x; select \"c;d\" -- e;f\n/* g;h */",
		"select $tag$ ; $tag$, $1 from t",
		"",
		"select 'unterminated",
	}
	for _, d := range []Dialect{Postgres, MySQL, MSSQL, SQLite} {
		for _, in := range inputs {
			var b strings.Builder
			for _, tok := range Tokenize(d, in) {
				b.WriteString(tok.Text)
			}
			if b.String() != in {
				t.Errorf("%s: tokens do not rebuild %q", d, in)
			}
		}
	}
}

func TestCountStatements(t *testing.T) {
	cases := []struct {
		d    Dialect
		sql  string
		want int
	}{
		{Postgres, "select 1", 1},
		{Postgres, "select 1;", 1},
		{Postgres, "select 1; ; -- trailing\n", 1},
		{Postgres, "select 1; drop table x", 2},
		{Postgres, "select ';' as a, \"b;c\" from t", 1},
		{Postgres, "select $$ ; $$ , $body$ a;b $body$", 1},
		{Postgres, "select $1, $2 from t; select 2", 2},
		{Postgres, "select E'it\\'s ; fine'", 1},
		{Postgres, "select 'it''s ; fine'", 1},
		{Postgres, "/* outer /* nested; */ still comment; */ select 1", 1},
		{Postgres, "-- only a comment; really", 0},
		{MySQL, "select 'a\\';b' from t", 1},
		{MySQL, "select \"a;b\" # comment; here\n", 1},
		{MySQL, "select `we;ird` from t; delete from t", 2},
		{MSSQL, "select [a;b] from t", 1},
		{MSSQL, "select 1\nGO\nselect 2", 2},
		{MSSQL, "select 1\n  go 5  \nselect 2", 2},
		{MSSQL, "select 1 as go_column", 1},
		{MSSQL, "select 'GO' \n", 1},
		{SQLite, "select [x;y], `z;w`, \"q;r\" from t", 1},
		{SQLite, "select 1; select 2;", 2},
	}
	for _, tc := range cases {
		if got := CountStatements(tc.d, tc.sql); got != tc.want {
			t.Errorf("%s %q: got %d, want %d (%q)", tc.d, tc.sql, got, tc.want, Statements(tc.d, tc.sql))
		}
	}
}

func TestTokenKinds(t *testing.T) {
	toks := Tokenize(Postgres, "select 'x' -- c\n from \"T\";")
	var kinds []Kind
	for _, tok := range toks {
		kinds = append(kinds, tok.Kind)
	}
	want := []Kind{Code, String, Code, Comment, Code, QuotedIdent, Semicolon}
	if len(kinds) != len(want) {
		t.Fatalf("kinds %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds %v, want %v", kinds, want)
		}
	}
}

func FuzzTokenizeRebuildsInput(f *testing.F) {
	for _, s := range []string{"select 1; select '2'", "$a$ x $a$", "/* /* */", "[x", "go\n"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		for _, d := range []Dialect{Postgres, MySQL, MSSQL, SQLite} {
			var b strings.Builder
			for _, tok := range Tokenize(d, in) {
				b.WriteString(tok.Text)
			}
			if b.String() != in {
				t.Fatalf("%s: tokens do not rebuild %q", d, in)
			}
		}
	})
}
