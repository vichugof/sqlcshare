// Package sqlcshare expands reusable SQL fragments into plain SQL files that
// sqlc compiles unchanged.
//
// A fragment is a named piece of SQL (a column list, a WHERE predicate, a
// JOIN block, a CTE body, or raw SQL) defined once in a fragment file:
//
//	-- fragment: visible_author(a) :where
//	{a}.deleted_at IS NULL AND {a}.org_id = sqlc.arg(org_id);
//
// A query source references it with a block-comment directive. Text between
// the directive and /*%end*/ is fallback text that keeps the source a complete
// statement for editors and other SQL tools; it is discarded on expansion:
//
//	-- name: CountAuthors :one
//	SELECT count(*) FROM authors a
//	WHERE /*%where visible_author(a)*/ TRUE /*%end*/;
//
// Expansion is text splicing at byte offsets. The expander never parses SQL,
// so sqlc's annotations and macros pass through untouched and sqlc types every
// expanded query at its use site. Every expansion records a SourceMap so that
// sqlc's file:line:col diagnostics can be mapped back to the source or the
// fragment file.
//
// A `-- derive: CountAuthors :count` comment under a query's `-- name:` line
// makes the expander write the query's count as a second statement: the
// SELECT list replaced by count(*) and the paging clauses dropped. See
// Derivation.
//
// Generate ties it together: it loads fragments, expands every source file
// into an output directory that sqlc.yaml lists under queries, and keeps a
// manifest there so that stale outputs are removed on the next run. Package
// gogen then reads the Go code sqlc generated and writes shared parameter
// structs for the fragments.
package sqlcshare

import (
	"fmt"
	"sort"
	"strings"
)

// Engine selects the dialect rules used by the scanner and by the lint
// checks. The names match the engine names in sqlc.yaml.
type Engine uint8

const (
	engineUnset Engine = iota
	PostgreSQL
	MySQL
	SQLite
)

// String returns the engine name as written in sqlc.yaml.
func (e Engine) String() string {
	switch e {
	case PostgreSQL:
		return "postgresql"
	case MySQL:
		return "mysql"
	case SQLite:
		return "sqlite"
	}
	return fmt.Sprintf("Engine(%d)", uint8(e))
}

// ParseEngine parses an engine name as written in sqlc.yaml.
func ParseEngine(name string) (Engine, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "postgresql", "postgres":
		return PostgreSQL, nil
	case "mysql":
		return MySQL, nil
	case "sqlite", "sqlite3":
		return SQLite, nil
	}
	return engineUnset, fmt.Errorf("unknown engine %q (want postgresql, mysql or sqlite)", name)
}

// Kind is a fragment kind. It decides how a fragment body is wrapped when it
// is spliced into a query.
type Kind uint8

const (
	kindUnset Kind = iota
	// KindColumns is a SELECT list or part of one. It is spliced verbatim.
	KindColumns
	// KindWhere is a predicate. It is spliced wrapped in parentheses, so an OR
	// inside the fragment cannot change the precedence of the enclosing WHERE.
	KindWhere
	// KindJoins is one or more JOIN clauses. It is spliced verbatim.
	KindJoins
	// KindCTE is the body of a common table expression. It is spliced as
	// `name AS (body)`, where name is the fragment name, inside a WITH clause
	// written by the query.
	KindCTE
	// KindSQL is raw SQL with no wrapping rule.
	KindSQL
)

var kindNames = [...]string{kindUnset: "", KindColumns: "columns", KindWhere: "where", KindJoins: "joins", KindCTE: "cte", KindSQL: "sql"}

// String returns the kind name as written after the colon in a fragment
// header or after the percent sign in a directive.
func (k Kind) String() string {
	if int(k) < len(kindNames) && kindNames[k] != "" {
		return kindNames[k]
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// ParseKind parses a kind name.
func ParseKind(name string) (Kind, bool) {
	for k, n := range kindNames {
		if n != "" && n == name {
			return Kind(k), true
		}
	}
	return kindUnset, false
}

// Pos is a position in a source or fragment file. Line and Col are 1-based
// and Col counts bytes, like sqlc's own diagnostics. A zero Line means the
// position is unknown.
type Pos struct {
	File string
	Line int
	Col  int
}

// String formats the position as file:line:col, omitting what is unknown.
func (p Pos) String() string {
	switch {
	case p.Line == 0:
		return p.File
	case p.Col == 0:
		return fmt.Sprintf("%s:%d", p.File, p.Line)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Severity classifies a Diagnostic.
type Severity uint8

const (
	severityUnset Severity = iota
	// Error means the expansion is unusable and nothing is written.
	Error
	// Warning means the expansion was produced but is likely wrong at runtime.
	Warning
	// Info is worth knowing and is printed only on request.
	Info
)

// String returns "error", "warning" or "info".
func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	case Info:
		return "info"
	}
	return fmt.Sprintf("Severity(%d)", uint8(s))
}

// Diagnostic is one message about a source or fragment file.
type Diagnostic struct {
	Pos      Pos
	Severity Severity
	Message  string
}

// String formats the diagnostic as "file:line:col: severity: message".
func (d Diagnostic) String() string {
	if d.Pos.File == "" && d.Pos.Line == 0 {
		return d.Severity.String() + ": " + d.Message
	}
	return d.Pos.String() + ": " + d.Severity.String() + ": " + d.Message
}

// Diagnostics is a list of diagnostics. It implements error so that a
// failing operation can return every problem at once.
type Diagnostics []Diagnostic

// Error joins the diagnostics with newlines.
func (ds Diagnostics) Error() string {
	lines := make([]string, len(ds))
	for i, d := range ds {
		lines[i] = d.String()
	}
	return strings.Join(lines, "\n")
}

// HasErrors reports whether any diagnostic has severity Error.
func (ds Diagnostics) HasErrors() bool {
	for _, d := range ds {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

// UpTo returns the diagnostics whose severity is max or more severe. UpTo(Warning)
// returns errors and warnings.
func (ds Diagnostics) UpTo(max Severity) Diagnostics {
	var out Diagnostics
	for _, d := range ds {
		if d.Severity <= max {
			out = append(out, d)
		}
	}
	return out
}

func (ds *Diagnostics) add(pos Pos, sev Severity, format string, args ...any) {
	*ds = append(*ds, Diagnostic{Pos: pos, Severity: sev, Message: fmt.Sprintf(format, args...)})
}

// sortDiagnostics orders diagnostics by position, then severity, so output is
// deterministic regardless of the order in which checks ran.
func sortDiagnostics(ds Diagnostics) {
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		if a.Pos.File != b.Pos.File {
			return a.Pos.File < b.Pos.File
		}
		if a.Pos.Line != b.Pos.Line {
			return a.Pos.Line < b.Pos.Line
		}
		if a.Pos.Col != b.Pos.Col {
			return a.Pos.Col < b.Pos.Col
		}
		return a.Severity < b.Severity
	})
}

// lineIndex maps byte offsets to 1-based line and column numbers.
type lineIndex struct {
	starts []int
}

func newLineIndex(text []byte) *lineIndex {
	starts := []int{0}
	for i, c := range text {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &lineIndex{starts: starts}
}

func (li *lineIndex) lineCol(off int) (line, col int) {
	i := sort.Search(len(li.starts), func(i int) bool { return li.starts[i] > off }) - 1
	return i + 1, off - li.starts[i] + 1
}

// textSource is a piece of text with a known position in a file: a whole
// file, or a fragment body embedded in a fragment file.
type textSource struct {
	file string
	text []byte
	li   *lineIndex
	base Pos
}

func newTextSource(file string, text []byte, base Pos) *textSource {
	return &textSource{file: file, text: text, li: newLineIndex(text), base: base}
}

// pos returns the file position of byte offset off within the text.
func (s *textSource) pos(off int) Pos {
	line, col := s.li.lineCol(off)
	if line == 1 {
		col += s.base.Col - 1
	}
	return Pos{File: s.file, Line: line + s.base.Line - 1, Col: col}
}

// firstNonSpace returns the offset of the first byte in src[start:end] that
// is not whitespace, or -1.
func firstNonSpace(src []byte, start, end int) int {
	for i := start; i < end; i++ {
		switch src[i] {
		case ' ', '\t', '\r', '\n', '\f', '\v':
		default:
			return i
		}
	}
	return -1
}
