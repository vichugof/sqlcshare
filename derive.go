package sqlcshare

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// A `-- derive: Name :count` comment in the lead of a statement makes the
// expander write a second statement after it: the same statement with its
// SELECT list replaced by count(*) and its ORDER BY, LIMIT, OFFSET, FETCH
// and FOR clauses dropped. A statement that uses DISTINCT, GROUP BY, HAVING,
// WINDOW or a set operation is counted through a derived table instead,
// `SELECT count(*) FROM (<statement>) AS sqlcshare_count`, so that the count
// is the number of rows the statement returns. The analysis runs over the
// expanded text, so fragments are seen through, and the cut points are mapped
// back to the source so that the derived statement is expanded from the same
// source ranges and keeps an exact source map.

var (
	tailWords = set("ORDER", "LIMIT", "OFFSET", "FETCH", "FOR")
	wrapWords = set("GROUP", "HAVING", "WINDOW", "UNION", "INTERSECT", "EXCEPT")
	dmlWords  = set("INSERT", "UPDATE", "DELETE", "VALUES", "TABLE", "MERGE")
)

// word is an identifier or keyword at parenthesis depth 0 in a statement.
type word struct {
	text       string // upper-cased
	start, end int
}

func isWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isWordChar(c byte) bool {
	return isWordStart(c) || (c >= '0' && c <= '9')
}

// depthZeroWords lists the words at parenthesis depth 0 in the code regions
// of text. A word preceded by @, : or . is a parameter or a qualified name,
// not a keyword, and is skipped.
func depthZeroWords(engine Engine, text string) []word {
	var words []word
	depth := 0
	for _, t := range scanSQL(engine, []byte(text)) {
		if t.kind != tokCode {
			continue
		}
		for i := t.start; i < t.end; {
			c := text[i]
			switch {
			case c == '(':
				depth++
				i++
			case c == ')':
				if depth > 0 {
					depth--
				}
				i++
			case isWordChar(c):
				j := i + 1
				for j < t.end && (isWordChar(text[j]) || text[j] == '.' && c >= '0' && c <= '9') {
					j++
				}
				prev := byte(' ')
				if i > 0 {
					prev = text[i-1]
				}
				if depth == 0 && isWordStart(c) && prev != '@' && prev != ':' && prev != '.' && prev != '$' {
					words = append(words, word{strings.ToUpper(text[i:j]), i, j})
				}
				i = j
			default:
				i++
			}
		}
	}
	return words
}

// countPlan says how to rewrite a statement into its count. Offsets are in
// the expanded statement body.
type countPlan struct {
	wrap                   bool     // count through a derived table
	reasons                []string // why wrap is set
	selectStart, selectEnd int      // the main SELECT keyword
	fromStart              int      // its FROM keyword
	tailStart              int      // the first dropped clause, or len(body)
}

// planCount analyses an expanded statement body.
func planCount(engine Engine, body string) (countPlan, error) {
	ws := depthZeroWords(engine, body)
	p := countPlan{tailStart: len(body), fromStart: -1}
	if len(ws) == 0 {
		return p, errors.New("the statement is empty")
	}
	i := 0
	switch ws[0].text {
	case "SELECT":
	case "WITH":
		for i = 1; i < len(ws) && ws[i].text != "SELECT"; i++ {
			if dmlWords[ws[i].text] {
				return p, fmt.Errorf("the statement is %s, not SELECT", ws[i].text)
			}
		}
		if i == len(ws) {
			return p, errors.New("no SELECT follows the WITH clause")
		}
	default:
		return p, fmt.Errorf("the statement starts with %s, not SELECT", ws[0].text)
	}
	p.selectStart, p.selectEnd = ws[i].start, ws[i].end
	for j := i + 1; j < len(ws); j++ {
		wt := ws[j].text
		if p.fromStart < 0 {
			switch wt {
			case "DISTINCT":
				p.reasons = append(p.reasons, "DISTINCT")
			case "FROM":
				p.fromStart = ws[j].start
			}
			continue
		}
		if wrapWords[wt] {
			p.reasons = append(p.reasons, wt)
			continue
		}
		if tailWords[wt] && (wt != "OFFSET" || offsetIsClause(body, ws[j].end)) {
			p.tailStart = ws[j].start
			break
		}
	}
	if p.fromStart < 0 {
		return p, errors.New("the SELECT has no FROM clause")
	}
	p.wrap = len(p.reasons) > 0
	return p, nil
}

// offsetIsClause reports whether the OFFSET ending at end starts a clause,
// which is followed by a number or a parameter, rather than naming a column,
// which MySQL allows.
func offsetIsClause(body string, end int) bool {
	rest := strings.TrimLeft(body[end:], " \t\r\n\f\v")
	if rest == "" {
		return false
	}
	switch c := rest[0]; {
	case c >= '0' && c <= '9', c == '?', c == '$', c == '@', c == ':':
		return true
	}
	return strings.HasPrefix(rest, "sqlc.")
}

// bodyMapper maps offsets in an expanded statement body back to the source
// file and to the directives that produced them.
type bodyMapper struct {
	ts   *textSource
	segs []Segment
	offs []int // start offset of each segment in the body
	uses []Use
}

func newBodyMapper(ts *textSource, body *writer, uses []Use) *bodyMapper {
	return &bodyMapper{ts: ts, segs: body.segs, offs: body.segOffsets(), uses: uses}
}

// source returns the source offset of the text at body offset off, which
// must have been copied from the source file.
func (m *bodyMapper) source(off int) (int, bool) {
	i := sort.Search(len(m.offs), func(i int) bool { return m.offs[i] > off }) - 1
	if i < 0 {
		return 0, false
	}
	s := m.segs[i]
	if s.Kind != SegSource || s.Src.File != m.ts.file || s.Src.Line < 1 || s.Src.Line > len(m.ts.li.starts) {
		return 0, false
	}
	return m.ts.li.starts[s.Src.Line-1] + s.Src.Col - 1 + (off - m.offs[i]), true
}

// use returns the directive of the statement whose expansion contains body
// offset off, or nil.
func (m *bodyMapper) use(off int) *Use {
	for i := range m.uses {
		if u := &m.uses[i]; u.Depth == 0 && off >= u.outStart && off < u.outEnd {
			return u
		}
	}
	return nil
}

// deriveStatement writes the statement that a derive comment asks for after
// the statement it was found on, whose expanded body is body.
func (x *expansion) deriveStatement(w *writer, ts *textSource, toks []token, sp stmtSpan, body *writer, d deriveSpec) {
	src := ts.text
	at := ts.pos(d.start)
	fail := func(format string, args ...any) {
		x.diags.add(at, Error, "derive %s: %s", d.name, fmt.Sprintf(format, args...))
	}
	base := &x.stmts[len(x.stmts)-1]
	switch {
	case d.kind != "count":
		fail("unknown derivation :%s; only :count is supported", d.kind)
		return
	case !identRE.MatchString(d.name):
		fail("%q is not a valid query name", d.name)
		return
	case sp.name == "":
		fail("the statement has no -- name: comment")
		return
	case toks[sp.lastTok].kind != tokSemicolon:
		fail("the statement must end with a semicolon")
		return
	}
	for _, s := range x.stmts {
		if s.Name == d.name {
			fail("a statement named %q already exists at %s", d.name, s.Pos)
			return
		}
	}
	text := body.String()
	plan, err := planCount(x.e.Engine, text)
	if err != nil {
		fail("%v", err)
		return
	}
	m := newBodyMapper(ts, body, base.Uses)
	keyword := func(off int, what string) (int, bool) {
		if u := m.use(off); u != nil {
			fail("the %s keyword comes from fragment %q, so the statement cannot be rewritten", what, u.Name)
			return 0, false
		}
		so, ok := m.source(off)
		if !ok {
			fail("the %s keyword cannot be located in the source", what)
		}
		return so, ok
	}
	selectStart, ok := keyword(plan.selectStart, "SELECT")
	if !ok {
		return
	}
	selectEnd := selectStart + (plan.selectEnd - plan.selectStart)
	fromStart := 0
	if !plan.wrap {
		if fromStart, ok = keyword(plan.fromStart, "FROM"); !ok {
			return
		}
	}
	keepEnd := toks[sp.lastTok].start
	if plan.tailStart < len(text) {
		if u := m.use(plan.tailStart); u != nil {
			first := ""
			if ws := depthZeroWords(x.e.Engine, text[u.outStart:u.outEnd]); len(ws) > 0 {
				first = ws[0].text
			}
			if !tailWords[first] {
				fail("fragment %q holds %s together with other clauses, so the clause cannot be dropped", u.Name, strings.ToUpper(wordAt(text, plan.tailStart)))
				return
			}
			keepEnd = u.srcStart
		} else if keepEnd, ok = m.source(plan.tailStart); !ok {
			fail("the %s clause cannot be located in the source", strings.ToUpper(wordAt(text, plan.tailStart)))
			return
		}
	}
	for keepEnd > selectEnd && isSpace(src[keepEnd-1]) {
		keepEnd--
	}
	endsInLineComment := false
	for _, t := range toks[sp.firstTok : sp.lastTok+1] {
		if t.kind == tokLineComment && keepEnd > t.start && keepEnd <= t.end {
			endsInLineComment = true
		}
	}

	bw := newWriter()
	bw.stmt = d.name
	x.uses = nil
	stmtToks := toks[sp.firstTok : sp.lastTok+1]
	from := sp.bodyStart
	gen := func(s string) { bw.write(s, ts.pos(selectStart), SegGenerated, "") }
	switch {
	case plan.wrap && selectStart == sp.bodyStart:
		gen("SELECT")
		if x.e.Marker {
			gen(" " + MarkerComment)
		}
		gen(" count(*) FROM (")
		x.expandRange(bw, ts, stmtToks, selectStart, keepEnd, 0, nil, nil)
		gen(") AS sqlcshare_count")
	case plan.wrap:
		if x.e.Marker {
			x.writeMarker(bw, ts, toks, sp, &from)
		}
		x.expandRange(bw, ts, stmtToks, from, selectStart, 0, nil, nil)
		gen("SELECT count(*) FROM (")
		x.expandRange(bw, ts, stmtToks, selectStart, keepEnd, 0, nil, nil)
		gen(") AS sqlcshare_count")
	default:
		if x.e.Marker {
			x.writeMarker(bw, ts, toks, sp, &from)
		}
		x.expandRange(bw, ts, stmtToks, from, selectEnd, 0, nil, nil)
		gen(" count(*) ")
		x.expandRange(bw, ts, stmtToks, fromStart, keepEnd, 0, nil, nil)
	}
	if endsInLineComment {
		gen("\n")
	}
	bw.write(";", ts.pos(toks[sp.lastTok].start), SegGenerated, "")

	stmt := Statement{Name: d.name, Pos: at, Uses: x.uses, Derived: DeriveCount, DerivedFrom: sp.name}
	x.finishStatement(bw, &stmt)
	if plan.wrap {
		x.diags.add(at, Info, "derive %s: counted through a derived table because the statement uses %s", d.name, strings.Join(plan.reasons, ", "))
	}
	prevStmt := w.stmt
	w.stmt = d.name
	w.write("\n\n", ts.pos(sp.end), SegGenerated, "")
	w.write("-- name: "+d.name+" :one\n", at, SegGenerated, "")
	if !x.e.NoProvenance {
		w.write(provenanceLine(ts.file, at.Line, "count of "+sp.name, stmt.DirectUses())+"\n", at, SegGenerated, "")
	}
	w.stmt = prevStmt
	w.append(bw)
	x.stmts = append(x.stmts, stmt)
}

// wordAt returns the word starting at off in text.
func wordAt(text string, off int) string {
	end := off
	for end < len(text) && isWordChar(text[end]) {
		end++
	}
	return text[off:end]
}

func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '\f', '\v':
		return true
	}
	return false
}
