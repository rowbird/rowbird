// Package sqlscan tokenizes SQL just enough to tell code apart from string literals, quoted
// identifiers and comments. Rowbird uses it to count statements (ADR-0013) and, later, to find
// query parameters without touching text inside literals. It is not a parser.
package sqlscan

import (
	"strings"
	"unicode"
)

// Dialect selects the quoting and comment rules.
type Dialect string

// Supported dialects.
const (
	Postgres Dialect = "postgres"
	MySQL    Dialect = "mysql"
	MSSQL    Dialect = "mssql"
	SQLite   Dialect = "sqlite"
)

// Kind is the kind of a token.
type Kind int

// Token kinds.
const (
	Code Kind = iota
	String
	QuotedIdent
	Comment
	Semicolon
	// BatchSeparator is a line containing only GO (SQL Server tools).
	BatchSeparator
)

// Token is a slice of the input. Text is exact, so concatenating all tokens rebuilds the input.
type Token struct {
	Kind  Kind
	Text  string
	Start int
}

// Tokenize splits sql into tokens for dialect. Unterminated literals and comments extend to the end
// of the input, which is what the database would reject anyway.
func Tokenize(d Dialect, sql string) []Token {
	var toks []Token
	codeStart := 0
	flush := func(end int) {
		if end > codeStart {
			toks = append(toks, Token{Kind: Code, Text: sql[codeStart:end], Start: codeStart})
		}
	}
	emit := func(kind Kind, start, end int) {
		flush(start)
		toks = append(toks, Token{Kind: kind, Text: sql[start:end], Start: start})
		codeStart = end
	}

	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '-' && at(sql, i+1) == '-':
			end := lineEnd(sql, i)
			emit(Comment, i, end)
			i = end
		case c == '#' && d == MySQL:
			end := lineEnd(sql, i)
			emit(Comment, i, end)
			i = end
		case c == '/' && at(sql, i+1) == '*':
			end := blockCommentEnd(sql, i, d == Postgres)
			emit(Comment, i, end)
			i = end
		case c == '\'':
			backslash := d == MySQL || (d == Postgres && i > 0 && (sql[i-1] == 'E' || sql[i-1] == 'e') && isStartOfWord(sql, i-1))
			end := quotedEnd(sql, i, '\'', backslash)
			emit(String, i, end)
			i = end
		case c == '"':
			kind := QuotedIdent
			if d == MySQL {
				kind = String // double quotes are strings unless ANSI_QUOTES is on
			}
			end := quotedEnd(sql, i, '"', d == MySQL)
			emit(kind, i, end)
			i = end
		case c == '`' && (d == MySQL || d == SQLite):
			end := quotedEnd(sql, i, '`', false)
			emit(QuotedIdent, i, end)
			i = end
		case c == '[' && (d == MSSQL || d == SQLite):
			end := quotedEnd(sql, i, ']', false)
			emit(QuotedIdent, i, end)
			i = end
		case c == '$' && d == Postgres:
			if tag, ok := dollarTag(sql, i); ok {
				end := strings.Index(sql[i+len(tag):], tag)
				if end < 0 {
					end = len(sql)
				} else {
					end += i + 2*len(tag)
				}
				emit(String, i, end)
				i = end
			} else {
				i++
			}
		case c == ';':
			emit(Semicolon, i, i+1)
			i++
		case d == MSSQL && isLineStart(sql, i) && isGoLine(sql, i):
			end := lineEnd(sql, i)
			emit(BatchSeparator, i, end)
			i = end
		default:
			i++
		}
	}
	flush(len(sql))
	return toks
}

func at(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func lineEnd(s string, i int) int {
	if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
		return i + j
	}
	return len(s)
}

func blockCommentEnd(s string, i int, nested bool) int {
	depth := 0
	for j := i; j < len(s)-1; j++ {
		switch {
		case s[j] == '/' && s[j+1] == '*':
			if depth == 0 || nested {
				depth++
			}
			j++
		case s[j] == '*' && s[j+1] == '/':
			depth--
			j++
			if depth == 0 {
				return j + 1
			}
		}
	}
	return len(s)
}

// quotedEnd finds the end of a literal opened at s[i]; a doubled quote is an escaped quote.
func quotedEnd(s string, i int, closer byte, backslash bool) int {
	for j := i + 1; j < len(s); j++ {
		switch {
		case backslash && s[j] == '\\':
			j++
		case s[j] == closer:
			if at(s, j+1) == closer {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(s)
}

// dollarTag recognizes $$ or $tag$ (Postgres dollar quoting); $1 placeholders are not tags.
func dollarTag(s string, i int) (string, bool) {
	for j := i + 1; j < len(s); j++ {
		c := s[j]
		if c == '$' {
			return s[i : j+1], true
		}
		if !isWordChar(c) || (j == i+1 && unicode.IsDigit(rune(c))) {
			return "", false
		}
	}
	return "", false
}

func isStartOfWord(s string, i int) bool {
	return i == 0 || !isWordChar(s[i-1])
}

func isWordChar(c byte) bool {
	return c == '_' || unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c))
}

func isLineStart(s string, i int) bool {
	for j := i - 1; j >= 0; j-- {
		switch s[j] {
		case ' ', '\t', '\r':
			continue
		case '\n':
			return true
		default:
			return false
		}
	}
	return true
}

func isGoLine(s string, i int) bool {
	line := strings.TrimSpace(s[i:lineEnd(s, i)])
	if len(line) < 2 || !strings.EqualFold(line[:2], "go") {
		return false
	}
	rest := strings.TrimSpace(line[2:])
	for _, r := range rest {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// Statements splits sql into statements at top-level semicolons and batch separators, dropping
// empty ones (whitespace and comments only).
func Statements(d Dialect, sql string) []string {
	var out []string
	var cur strings.Builder
	meaningful := false
	finish := func() {
		if meaningful {
			out = append(out, strings.TrimSpace(cur.String()))
		}
		cur.Reset()
		meaningful = false
	}
	for _, t := range Tokenize(d, sql) {
		switch t.Kind {
		case Semicolon, BatchSeparator:
			finish()
		case Comment:
			cur.WriteString(t.Text)
		default:
			cur.WriteString(t.Text)
			if strings.TrimSpace(t.Text) != "" {
				meaningful = true
			}
		}
	}
	finish()
	return out
}

// CountStatements returns how many statements sql contains.
func CountStatements(d Dialect, sql string) int { return len(Statements(d, sql)) }
