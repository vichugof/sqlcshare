// Package mysql checks MySQL syntax with sqlc-dev/marino, the sqlc project's
// fork of the TiDB parser that sqlc uses for MySQL. sqlc's macros parse as
// function calls, so expanded statements are checked as written; like sqlc,
// the parser rejects a macro in a LIMIT clause, where only ? works.
package mysql

import (
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/sqlc-dev/marino/parser"
	"github.com/vichugof/sqlcshare"
)

// Checker implements sqlcshare.Checker for MySQL.
type Checker struct{}

var pool = sync.Pool{New: func() any { return parser.New() }}

// nearRE matches the parser's message, `line L column C near "text"`, where C
// is the column just past the token the parser stopped at and text is the
// rest of the input, quoted with Go syntax.
var nearRE = regexp.MustCompile(`^line (\d+) column (\d+) near ("(?:[^"\\]|\\.)*")`)

// Check parses sql and reports the first syntax error with its byte offset.
func (Checker) Check(sql string) *sqlcshare.SyntaxError {
	p := pool.Get().(*parser.Parser)
	defer pool.Put(p)
	_, _, err := p.ParseSQL(sql)
	if err == nil {
		return nil
	}
	msg := err.Error()
	m := nearRE.FindStringSubmatch(msg)
	if m == nil {
		return &sqlcshare.SyntaxError{Offset: -1, Message: msg}
	}
	line, _ := strconv.Atoi(m[1])
	col, _ := strconv.Atoi(m[2])
	near, uerr := strconv.Unquote(m[3])
	off := lineStart(sql, line)
	limit := min(off+max(col, 1), len(sql))
	if k := -1; uerr == nil && near != "" {
		// The near text starts at the offending token, before the reported column.
		k = lastIndexBefore(sql, near, limit)
		if k >= off {
			off = k
		} else {
			off = min(off+max(col-1, 0), len(sql))
		}
	} else {
		off = min(off+max(col-1, 0), len(sql))
	}
	shown := near
	if i := strings.IndexByte(shown, '\n'); i >= 0 {
		shown = shown[:i]
	}
	if len(shown) > 40 {
		shown = shown[:40] + "..."
	}
	return &sqlcshare.SyntaxError{Offset: off, Message: "syntax error near " + strconv.Quote(shown)}
}

// lineStart returns the byte offset of the 1-based line.
func lineStart(s string, line int) int {
	off := 0
	for i := 1; i < line; i++ {
		k := strings.IndexByte(s[off:], '\n')
		if k < 0 {
			return len(s)
		}
		off += k + 1
	}
	return off
}

// lastIndexBefore returns the start of the last occurrence of sub in s that
// begins before limit, or -1.
func lastIndexBefore(s, sub string, limit int) int {
	last := -1
	for from := 0; from < limit; {
		k := strings.Index(s[from:], sub)
		if k < 0 || from+k >= limit {
			break
		}
		last = from + k
		from = last + 1
	}
	return last
}
