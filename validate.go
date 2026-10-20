package sqlcshare

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// SyntaxError is what a Checker reports for text it rejects. Offset is the
// 0-based byte offset of the error in the checked text, or -1 when unknown.
type SyntaxError struct {
	Offset  int
	Message string
}

func (e *SyntaxError) Error() string { return e.Message }

// Checker validates the syntax of SQL text for one engine. The packages under
// check wrap the parsers sqlc itself uses, so what they accept parses in sqlc,
// and they report exact positions where sqlc's own messages do not.
type Checker interface {
	Check(sql string) *SyntaxError
}

// CheckerFunc adapts a function to the Checker interface.
type CheckerFunc func(sql string) *SyntaxError

// Check calls f.
func (f CheckerFunc) Check(sql string) *SyntaxError { return f(sql) }

// checkSyntax runs the expander's checker over one expanded statement body
// and reports a failure at the source or fragment position it came from.
func (x *expansion) checkSyntax(bw *writer, stmt *Statement) {
	text := bw.String()
	if strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), ";")) == "" {
		return
	}
	se := x.e.Checker.Check(text)
	if se == nil {
		return
	}
	pos, seg := originOf(text, bw.segs, se.Offset)
	if seg == nil {
		pos = stmt.Pos
	}
	where := ""
	if seg != nil && seg.Fragment != "" {
		where = " (in fragment " + seg.Fragment + ")"
	}
	x.diags.add(pos, Error, "syntax error: %s%s", se.Message, where)
}

// originOf maps a byte offset in expanded text to its origin through the
// text's segments. seg is nil when the offset is unknown.
func originOf(text string, segs []Segment, off int) (Pos, *Segment) {
	if off < 0 || len(text) == 0 {
		return Pos{}, nil
	}
	line, col := newLineIndex([]byte(text)).lineCol(min(off, len(text)-1))
	p, seg, ok := (&SourceMap{Segments: segs}).Origin(line, col)
	if !ok {
		return Pos{}, nil
	}
	return p, seg
}

// fragmentCheckFile names the synthetic source that CheckFragments expands.
const fragmentCheckFile = "(fragment check)"

// CheckFragments parses every fragment of lib with checker inside a template
// statement for its kind, with the fragment's own parameter names as
// arguments, and reports failures at their position in the fragment file.
// Fragments of kind sql have no template and are checked only where they are
// used. A nil lib or checker checks nothing.
func CheckFragments(engine Engine, lib *Library, checker Checker) Diagnostics {
	if lib == nil || checker == nil {
		return nil
	}
	var diags Diagnostics
	seen := map[Diagnostic]bool{}
	report := func(d Diagnostic) {
		if !seen[d] {
			seen[d] = true
			diags = append(diags, d)
		}
	}
	ex := &Expander{Engine: engine, Lib: lib, NoProvenance: true, skipLint: true}
	for _, f := range lib.Fragments() {
		src, ok := fragmentTemplate(f)
		if !ok {
			continue
		}
		res, _ := ex.Expand(fragmentCheckFile, []byte(src))
		for _, d := range res.Diagnostics {
			if d.Pos.File == fragmentCheckFile {
				d.Pos = f.Pos
			}
			report(d)
		}
		if res.Diagnostics.HasErrors() {
			continue
		}
		text := string(res.Text)
		se := checker.Check(text)
		if se == nil {
			continue
		}
		pos, seg := originOf(text, res.Map.Segments, se.Offset)
		switch {
		case seg != nil && seg.Kind == SegFragment:
			report(Diagnostic{Pos: pos, Severity: Error, Message: "fragment \"" + seg.Fragment + "\": syntax error: " + se.Message})
		case seg != nil && seg.Kind == SegGenerated && seg.Fragment != "":
			// The error is at the wrapper the expander writes around the
			// body, so the body is incomplete: report its end.
			g, _ := lib.Get(seg.Fragment)
			end := newTextSource(g.Pos.File, []byte(g.Body), g.BodyPos).pos(len(g.Body))
			report(Diagnostic{Pos: end, Severity: Error, Message: "fragment \"" + g.Name + "\": syntax error at the end of the body: " + se.Message})
		default:
			report(Diagnostic{Pos: f.Pos, Severity: Error, Message: "fragment \"" + f.Name + "\" does not parse as a " + f.Kind.String() + " fragment: " + se.Message})
		}
	}
	sortDiagnostics(diags)
	return diags
}

// fragmentTemplate returns a statement that uses the fragment in the slot its
// kind is meant for.
func fragmentTemplate(f *Fragment) (string, bool) {
	use := "/*%" + f.Kind.String() + " " + f.Name
	if len(f.Params) > 0 {
		use += "(" + strings.Join(f.Params, ", ") + ")"
	}
	use += "*/"
	switch f.Kind {
	case KindColumns:
		return "SELECT " + use + " FROM sqlcshare_t;", true
	case KindWhere:
		return "SELECT 1 FROM sqlcshare_t WHERE " + use + ";", true
	case KindJoins:
		return "SELECT 1 FROM sqlcshare_t " + use + ";", true
	case KindCTE:
		return "WITH " + use + " SELECT 1 FROM " + f.Name + ";", true
	}
	return "", false
}

// Slot heuristics: a cheap check of the word before a directive against the
// kind of fragment it names. They produce warnings only, since the syntax
// check after expansion is the real test and these rules can be fooled.
var (
	trailingWordRE = regexp.MustCompile(`[A-Za-z0-9_]+$`)
	slotForbidden  = map[Kind]map[string]bool{
		KindWhere:   set("SELECT", "DISTINCT", "RETURNING", "FROM", "JOIN", "INTO", "SET", ","),
		KindColumns: set("WHERE", "AND", "OR", "ON", "HAVING", "JOIN", "FROM", "WITH"),
		KindJoins:   set("SELECT", "DISTINCT", "WHERE", "AND", "OR", "WITH", ",", "="),
	}
	cteAllowed = set("WITH", "RECURSIVE", ",")
	slotHints  = map[Kind]string{
		KindWhere:   "expected after WHERE, AND, OR, ON or HAVING",
		KindColumns: "expected in a SELECT list, after SELECT or a comma",
		KindJoins:   "expected after a table reference in FROM",
		KindCTE:     "expected inside a WITH clause, after WITH, RECURSIVE or a comma",
	}
)

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// slotWarning returns a warning when a directive of kind k follows prev, the
// previous significant word, in a position that cannot be right.
func slotWarning(k Kind, name, prev string) string {
	if prev == "" {
		return ""
	}
	bad := false
	switch k {
	case KindCTE:
		bad = !cteAllowed[prev]
	default:
		bad = slotForbidden[k][prev]
	}
	if !bad {
		return ""
	}
	return "fragment " + strconv.Quote(name) + " is a " + k.String() + " fragment but follows " + strconv.Quote(prev) + "; " + slotHints[k]
}

// prevWord returns the last significant word, or punctuation character,
// before token i, upper-cased. A string or quoted identifier is reported as a
// single quote. Comments are skipped.
func prevWord(src []byte, toks []token, i, from int) string {
	for j := i - 1; j >= 0; j-- {
		t := toks[j]
		switch t.kind {
		case tokLineComment, tokBlockComment:
			continue
		case tokCode:
			text := strings.TrimRight(string(src[max(t.start, from):t.end]), " \t\r\n\f\v")
			if text == "" {
				continue
			}
			if w := trailingWordRE.FindString(text); w != "" {
				return strings.ToUpper(w)
			}
			return text[len(text)-1:]
		default:
			return "'"
		}
	}
	return ""
}

var embedRE = regexp.MustCompile(`\bsqlc\.embed\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)`)

// RewriteMacros replaces sqlc's macros in the code regions of sql with plain
// SQL, the way sqlc's own preprocessor does before parsing: sqlc.arg,
// sqlc.narg and sqlc.slice become a placeholder ($n on PostgreSQL, ? on MySQL
// and SQLite) and sqlc.embed(t) becomes t.*. The returned function maps a
// byte offset in the rewritten text back to the original text; an offset
// inside a replacement maps to the start of the macro it replaced.
func RewriteMacros(engine Engine, sql string) (string, func(int) int) {
	type piece struct {
		newStart, oldStart, oldEnd int
		replaced                   bool
	}
	type match struct {
		start, end int
		text       string
	}
	toks := scanSQL(engine, []byte(sql))
	var ms []match
	for i, m := range findMacros(toks, sql) {
		ph := "?"
		if engine == PostgreSQL {
			ph = "$" + strconv.Itoa(i+1)
		}
		ms = append(ms, match{m.start, m.end, ph})
	}
	for _, m := range embedRE.FindAllStringSubmatchIndex(sql, -1) {
		if inCode(toks, m[0]) {
			ms = append(ms, match{m[0], m[1], sql[m[2]:m[3]] + ".*"})
		}
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].start < ms[j].start })
	var pieces []piece
	var b strings.Builder
	cursor := 0
	for _, m := range ms {
		if m.start > cursor {
			pieces = append(pieces, piece{b.Len(), cursor, m.start, false})
			b.WriteString(sql[cursor:m.start])
		}
		pieces = append(pieces, piece{b.Len(), m.start, m.end, true})
		b.WriteString(m.text)
		cursor = m.end
	}
	if cursor < len(sql) {
		pieces = append(pieces, piece{b.Len(), cursor, len(sql), false})
		b.WriteString(sql[cursor:])
	}
	back := func(off int) int {
		i := sort.Search(len(pieces), func(i int) bool { return pieces[i].newStart > off }) - 1
		if i < 0 {
			return 0
		}
		p := pieces[i]
		if p.replaced {
			return p.oldStart
		}
		return min(p.oldStart+(off-p.newStart), p.oldEnd)
	}
	return b.String(), back
}
