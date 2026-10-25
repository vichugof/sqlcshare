package sqlcshare

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// MarkerComment is the inline comment inserted after the first keyword of
// every statement when Expander.Marker is set. Unlike a comment on its own
// line, an inline comment survives into sqlc's query text, so a sqlc vet rule
// such as `!query.sql.contains("/* sqlcshare */")` can flag queries that
// bypassed the expander.
const MarkerComment = "/* sqlcshare */"

// DefaultMaxDepth bounds fragment nesting.
const DefaultMaxDepth = 16

// Expander expands the directives in query source files.
type Expander struct {
	Engine Engine
	Lib    *Library // nil means no fragments are defined
	// Marker inserts MarkerComment after the first keyword of every statement.
	Marker bool
	// NoProvenance suppresses the `-- sqlcshare: file:line (uses)` comment
	// that is otherwise inserted after the `-- name:` line of every statement
	// that used a fragment or was derived from another statement. sqlc turns
	// that comment into the Go doc comment of the generated method.
	NoProvenance bool
	// ParamLimit is the query_parameter_limit of the sqlc block that compiles
	// the output; 0 means sqlc's default of 1. It only affects a lint check.
	ParamLimit int
	// MaxDepth bounds fragment nesting; 0 means DefaultMaxDepth.
	MaxDepth int
	// Checker, when set, parses every expanded statement and reports syntax
	// errors at their source or fragment position. See the check packages.
	Checker Checker

	skipLint bool // set by CheckFragments, whose synthetic statements need no lint
}

// Use records one directive expanded in a statement, directly (Depth 0) or
// inside another fragment's body.
type Use struct {
	Kind  Kind
	Name  string
	Pos   Pos // position of the directive, in the source or fragment file
	Def   Pos // position of the fragment definition
	Depth int // 0 for a directive in the statement, 1 inside a fragment it used, and so on
	// Params lists the names of the sqlc parameters bound inside the
	// fragment's expansion, nested fragments included, in order of first
	// appearance. Positional reports that a placeholder without a name, `?`
	// on MySQL, is bound there too.
	Params     []string
	Positional bool

	outStart, outEnd int // range of the expansion in the statement body
	srcStart, srcEnd int // range of the directive and its fallback in the source
}

// Derivation says how a derived statement was built from another one.
type Derivation uint8

const (
	// DeriveNone marks a statement written in the source.
	DeriveNone Derivation = iota
	// DeriveCount is a `-- derive: Name :count` statement: the source
	// statement's SELECT list replaced by count(*) and its ORDER BY, LIMIT,
	// OFFSET, FETCH and FOR clauses dropped.
	DeriveCount
)

// String returns the name written after the colon in a derive comment.
func (d Derivation) String() string {
	if d == DeriveCount {
		return "count"
	}
	return ""
}

// Statement describes one statement of an expanded file.
type Statement struct {
	Name string // from the `-- name:` comment, or empty
	Pos  Pos    // position of the first byte of the statement body, or of the derive comment
	Uses []Use  // every directive expanded in this statement, nested ones included
	// Derived and DerivedFrom are set for a statement the expander wrote from
	// a `-- derive:` comment; DerivedFrom names the statement it came from.
	Derived     Derivation
	DerivedFrom string
}

// DirectUses returns the directives written in the statement itself.
func (s *Statement) DirectUses() []Use {
	var out []Use
	for _, u := range s.Uses {
		if u.Depth == 0 {
			out = append(out, u)
		}
	}
	return out
}

// Result is the expansion of one source file.
type Result struct {
	File        string
	Text        []byte
	Map         *SourceMap
	Statements  []Statement
	Diagnostics Diagnostics
}

var (
	nameLineRE   = regexp.MustCompile(`^(?:--|#)\s*name:\s*(\S+)\s+:\S+\s*$`)
	nameBlockRE  = regexp.MustCompile(`^/\*\s*name:\s*(\S+)\s+:\S+\s*\*/$`)
	deriveLineRE = regexp.MustCompile(`^(?:--|#)\s*derive:\s*(\S+)\s+:(\S+)\s*$`)
	wordRE       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
)

// Expand expands the directives in src, a query source file named file (the
// name is used in positions and in provenance comments). The returned error,
// when not nil, is the Diagnostics with severity Error; the Result is still
// returned so that partial output can be inspected.
func (e *Expander) Expand(file string, src []byte) (*Result, error) {
	if e.Engine == engineUnset {
		return nil, errors.New("sqlcshare: Expander.Engine must be set")
	}
	x := &expansion{e: e, lib: e.Lib, maxDepth: e.MaxDepth}
	if x.lib == nil {
		x.lib = NewLibrary()
	}
	if x.maxDepth <= 0 {
		x.maxDepth = DefaultMaxDepth
	}
	ts := newTextSource(file, src, Pos{File: file, Line: 1, Col: 1})
	toks := scanSQL(e.Engine, src)
	w := newWriter()
	spans, trailer := splitStatements(src, toks)
	for _, sp := range spans {
		x.expandStatement(w, ts, toks, sp)
	}
	w.write(string(src[trailer:]), ts.pos(trailer), SegSource, "")
	x.diags = dedupeDiagnostics(x.diags)
	res := &Result{
		File:        file,
		Text:        []byte(w.String()),
		Map:         &SourceMap{Source: file, Segments: w.segs},
		Statements:  x.stmts,
		Diagnostics: x.diags,
	}
	if x.diags.HasErrors() {
		return res, x.diags
	}
	return res, nil
}

type expansion struct {
	e        *Expander
	lib      *Library
	maxDepth int
	diags    Diagnostics
	stmts    []Statement
	uses     []Use // directives expanded in the statement being written
}

// stmtSpan locates one statement in a source file: its leading comments and
// whitespace, its body (up to and including the semicolon), the `-- name:`
// comment if any, and the `-- derive:` comments.
type stmtSpan struct {
	leadStart, bodyStart, end int
	nameStart, nameLineEnd    int // -1 when there is no name comment
	name                      string
	firstTok, lastTok         int // body token range, inclusive
	derives                   []deriveSpec
}

// deriveSpec is one `-- derive: Name :kind` comment in a statement's lead.
type deriveSpec struct {
	name           string
	kind           string
	start, lineEnd int // the comment line, newline included
}

func (sp *stmtSpan) setName(name string, start, lineEnd int) {
	sp.name, sp.nameStart, sp.nameLineEnd = name, start, lineEnd
}

// splitStatements finds the statements of src. The second result is the
// offset of the trailer: comments and whitespace after the last statement.
func splitStatements(src []byte, toks []token) ([]stmtSpan, int) {
	var spans []stmtSpan
	leadStart := 0
	i := 0
	for i < len(toks) {
		sp := stmtSpan{leadStart: leadStart, nameStart: -1, nameLineEnd: -1}
		j, ok := leadEnd(src, toks, i, &sp)
		if !ok {
			break
		}
		sp.firstTok = j
		k := j
		for k < len(toks) && toks[k].kind != tokSemicolon {
			k++
		}
		if k < len(toks) {
			sp.end, sp.lastTok = toks[k].end, k
		} else {
			sp.end, sp.lastTok = len(src), len(toks)-1
		}
		spans = append(spans, sp)
		leadStart, i = sp.end, sp.lastTok+1
	}
	return spans, leadStart
}

// leadEnd scans the comments and whitespace that precede a statement body,
// starting at token i, records the statement's name and derive comments, and
// returns the index of the first body token. ok is false when only comments
// and whitespace remain. A directive comment starts the body.
func leadEnd(src []byte, toks []token, i int, sp *stmtSpan) (int, bool) {
	for j := i; j < len(toks); j++ {
		t := toks[j]
		switch t.kind {
		case tokCode:
			if k := firstNonSpace(src, t.start, t.end); k >= 0 {
				sp.bodyStart = k
				return j, true
			}
		case tokLineComment:
			text := src[t.start:t.end]
			if m := nameLineRE.FindSubmatch(text); m != nil {
				sp.setName(string(m[1]), t.start, lineEndInclusive(src, t.end))
			} else if m := deriveLineRE.FindSubmatch(text); m != nil {
				sp.derives = append(sp.derives, deriveSpec{name: string(m[1]), kind: string(m[2]), start: t.start, lineEnd: lineEndInclusive(src, t.end)})
			}
		case tokBlockComment:
			text := src[t.start:t.end]
			if isDirectiveComment(string(text)) {
				sp.bodyStart = t.start
				return j, true
			}
			if m := nameBlockRE.FindSubmatch(text); m != nil {
				sp.setName(string(m[1]), t.start, lineEndInclusive(src, t.end))
			}
		default:
			sp.bodyStart = t.start
			return j, true
		}
	}
	return 0, false
}

func (x *expansion) expandStatement(w *writer, ts *textSource, toks []token, sp stmtSpan) {
	stmt := Statement{Name: sp.name, Pos: ts.pos(sp.bodyStart)}
	bw := newWriter()
	from := sp.bodyStart
	if x.e.Marker {
		if !x.writeMarker(bw, ts, toks, sp, &from) {
			x.diags.add(stmt.Pos, Warning, "marker not inserted: statement does not start with a keyword")
		}
	}
	x.uses = nil
	x.expandRange(bw, ts, toks[sp.firstTok:sp.lastTok+1], from, sp.end, 0, nil, nil)
	stmt.Uses = x.uses
	x.finishStatement(bw, &stmt)
	if direct := stmt.DirectUses(); len(direct) > 0 && !x.e.NoProvenance && sp.nameStart >= 0 {
		x.writeLead(w, ts, sp, provenanceLine(ts.file, ts.pos(sp.nameStart).Line, "", direct), direct[0].Pos)
	} else {
		x.writeLead(w, ts, sp, "", Pos{})
	}
	w.append(bw)
	x.stmts = append(x.stmts, stmt)
	for _, d := range sp.derives {
		x.deriveStatement(w, ts, toks, sp, bw, d)
	}
}

// writeMarker writes the first keyword of a statement body followed by the
// marker comment and advances *from past the keyword. It returns false when
// the body does not start with a keyword.
func (x *expansion) writeMarker(bw *writer, ts *textSource, toks []token, sp stmtSpan, from *int) bool {
	if toks[sp.firstTok].kind != tokCode {
		return false
	}
	word := wordRE.Find(ts.text[*from:sp.end])
	if word == nil {
		return false
	}
	bw.write(string(word), ts.pos(*from), SegSource, "")
	bw.write(" "+MarkerComment, ts.pos(*from), SegGenerated, "")
	*from += len(word)
	return true
}

// finishStatement attributes parameters to uses and runs the lint and syntax
// checks over one expanded statement body.
func (x *expansion) finishStatement(bw *writer, stmt *Statement) {
	attributeParams(x.e.Engine, bw.String(), stmt.Uses)
	if !x.e.skipLint {
		x.lint(bw, stmt)
	}
	if x.e.Checker != nil {
		x.checkSyntax(bw, stmt)
	}
}

// attributeParams fills Use.Params and Use.Positional from the parameter
// references found in the expanded body.
func attributeParams(engine Engine, body string, uses []Use) {
	if len(uses) == 0 {
		return
	}
	refs := findParams(engine, body)
	for i := range uses {
		u := &uses[i]
		u.Params, u.Positional = nil, false
		for _, r := range refs {
			if r.off < u.outStart || r.off >= u.outEnd {
				continue
			}
			if r.name == "" {
				u.Positional = true
			} else if !slices.Contains(u.Params, r.name) {
				u.Params = append(u.Params, r.name)
			}
		}
	}
}

// writeLead copies the comments and whitespace before a statement body,
// leaving out the derive comments, and inserts the provenance line, when not
// empty, after the name comment. provPos is the position the provenance line
// is attributed to.
func (x *expansion) writeLead(w *writer, ts *textSource, sp stmtSpan, provenance string, provPos Pos) {
	src := ts.text
	cursor := sp.leadStart
	if sp.nameLineEnd < 0 || sp.nameLineEnd > sp.bodyStart {
		provenance = ""
	}
	emit := func(to int) {
		if provenance != "" && sp.nameLineEnd >= cursor && sp.nameLineEnd <= to {
			w.write(string(src[cursor:sp.nameLineEnd]), ts.pos(cursor), SegSource, "")
			w.write(provenance+"\n", provPos, SegGenerated, "")
			cursor = sp.nameLineEnd
			provenance = ""
		}
		w.write(string(src[cursor:to]), ts.pos(cursor), SegSource, "")
		cursor = to
	}
	for _, d := range sp.derives {
		emit(d.start)
		cursor = d.lineEnd
	}
	emit(sp.bodyStart)
}

// provenanceLine formats the comment that records where an expanded statement
// came from. sqlc parses any @word in a comment as a flag, so none may appear.
func provenanceLine(file string, line int, derived string, uses []Use) string {
	var b strings.Builder
	fmt.Fprintf(&b, "-- sqlcshare: %s:%d (", strings.ReplaceAll(file, "@", "_"), line)
	if derived != "" {
		b.WriteString(derived)
	}
	for i, u := range uses {
		if i > 0 || derived != "" {
			b.WriteString(", ")
		}
		b.WriteString(u.Kind.String())
		b.WriteByte(' ')
		b.WriteString(u.Name)
	}
	b.WriteByte(')')
	return b.String()
}

// fragmentScope is set while expanding a fragment body: the fragment's name,
// used to attribute output text, and the values of its parameters.
type fragmentScope struct {
	name  string
	subst map[string]string
}

// expandRange copies ts.text[from:to] to w token by token, replacing every
// directive among toks and recording each in x.uses. Inside a fragment body
// (scope != nil) code regions are rewritten: {param} tokens are substituted
// and @name is normalised to sqlc.arg(name), each piece keeping its own
// source position.
func (x *expansion) expandRange(w *writer, ts *textSource, toks []token, from, to, depth int, stack []string, scope *fragmentScope) {
	src := ts.text
	kind, frag := SegSource, ""
	if scope != nil {
		kind, frag = SegFragment, scope.name
	}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		start, end := max(t.start, from), min(t.end, to)
		if start >= end {
			continue
		}
		if t.kind == tokBlockComment {
			text := string(src[t.start:t.end])
			if scope != nil {
				text = substitutePlaceholders(text, scope.subst)
			}
			d, ok, err := parseDirective(text)
			switch {
			case !ok:
			case err != nil:
				x.diags.add(ts.pos(t.start), Error, "%v", err)
			case d.end:
				x.diags.add(ts.pos(t.start), Error, "/*%%end*/ without a preceding directive")
			default:
				replaceEnd := t.end
				prev := prevWord(src, toks, i, from)
				if j := nextDirective(src, toks, i+1, to); j >= 0 {
					if nd, _, _ := parseDirective(string(src[toks[j].start:toks[j].end])); nd != nil && nd.end {
						replaceEnd = toks[j].end
						i = j
					}
				}
				usePos := ts.pos(t.start)
				if msg := slotWarning(d.kind, d.name, prev); msg != "" {
					x.diags.add(usePos, Warning, "%s", msg)
				}
				use := Use{Kind: d.kind, Name: d.name, Pos: usePos, Depth: depth, outStart: w.buf.Len(), srcStart: t.start, srcEnd: replaceEnd}
				if f, ok := x.expandUse(w, d, usePos, depth, stack); ok {
					use.Def = f.Pos
				} else {
					w.write(string(src[t.start:replaceEnd]), usePos, kind, frag)
				}
				use.outEnd = w.buf.Len()
				x.uses = append(x.uses, use)
				continue
			}
		}
		if t.kind == tokCode && scope != nil {
			x.writeRewritten(w, ts, start, end, scope)
		} else {
			w.write(string(src[start:end]), ts.pos(start), kind, frag)
		}
	}
}

// nextDirective returns the index of the next directive comment among toks
// starting at i and ending before to, or -1.
func nextDirective(src []byte, toks []token, i, to int) int {
	for ; i < len(toks); i++ {
		t := toks[i]
		if t.end > to {
			return -1
		}
		if t.kind == tokBlockComment && isDirectiveComment(string(src[t.start:t.end])) {
			return i
		}
	}
	return -1
}

// expandUse writes the expansion of one directive and returns the fragment
// it named. It returns false, after recording a diagnostic, when the
// directive cannot be expanded.
func (x *expansion) expandUse(w *writer, d *directive, usePos Pos, depth int, stack []string) (*Fragment, bool) {
	f, ok := x.lib.Get(d.name)
	if !ok {
		x.diags.add(usePos, Error, "unknown fragment %q", d.name)
		return nil, false
	}
	if f.Kind != d.kind {
		x.diags.add(usePos, Error, "fragment %q is defined as :%s at %s but used as %%%s", d.name, f.Kind, f.Pos, d.kind)
		return nil, false
	}
	if len(d.args) != len(f.Params) {
		x.diags.add(usePos, Error, "fragment %q takes %d argument(s) (%s), got %d", d.name, len(f.Params), strings.Join(f.Params, ", "), len(d.args))
		return nil, false
	}
	if slices.Contains(stack, f.Name) {
		x.diags.add(usePos, Error, "fragment cycle: %s", strings.Join(append(slices.Clone(stack), f.Name), " -> "))
		return nil, false
	}
	if depth >= x.maxDepth {
		x.diags.add(usePos, Error, "fragment nesting deeper than %d", x.maxDepth)
		return nil, false
	}
	scope := &fragmentScope{name: f.Name, subst: make(map[string]string, len(f.Params))}
	for i, p := range f.Params {
		scope.subst[p] = d.args[i]
	}
	prefix, suffix := wrap(f.Kind, f.Name)
	w.beginSplice(usePos, f.Name)
	w.write(prefix, usePos, SegGenerated, f.Name)
	bts := newTextSource(f.Pos.File, []byte(f.Body), f.BodyPos)
	x.expandRange(w, bts, scanSQL(x.e.Engine, bts.text), 0, len(f.Body), depth+1, slices.Concat(stack, []string{f.Name}), scope)
	w.write(suffix, usePos, SegGenerated, f.Name)
	w.endSplice()
	return f, true
}

// wrap returns the text written before and after a fragment body of kind k.
func wrap(k Kind, name string) (prefix, suffix string) {
	switch k {
	case KindWhere:
		return "(", ")"
	case KindCTE:
		return name + " AS (", ")"
	}
	return "", ""
}

// substitutePlaceholders replaces {param} tokens in text.
func substitutePlaceholders(text string, subst map[string]string) string {
	if len(subst) == 0 || !strings.Contains(text, "{") {
		return text
	}
	return placeholderRE.ReplaceAllStringFunc(text, func(s string) string {
		if v, ok := subst[s[1:len(s)-1]]; ok {
			return v
		}
		return s
	})
}

// codeRewrite is one replacement in a code region of a fragment body.
type codeRewrite struct {
	start, end int
	text       string
}

// codeRewrites lists the replacements to apply to a code region of a
// fragment body: {param} substitutions and @name normalisation, in order.
func codeRewrites(code string, subst map[string]string) []codeRewrite {
	var rs []codeRewrite
	if strings.Contains(code, "{") {
		for _, m := range placeholderRE.FindAllStringSubmatchIndex(code, -1) {
			if v, ok := subst[code[m[2]:m[3]]]; ok {
				rs = append(rs, codeRewrite{m[0], m[1], v})
			}
		}
	}
	if strings.Contains(code, "@") {
		for _, m := range atParamRE.FindAllStringSubmatchIndex(code, -1) {
			rs = append(rs, codeRewrite{m[4] - 1, m[5], "sqlc.arg(" + code[m[4]:m[5]] + ")"})
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].start < rs[j].start })
	return rs
}

// applyRewrites returns code with the rewrites applied.
func applyRewrites(code string, rs []codeRewrite) string {
	var b strings.Builder
	cursor := 0
	for _, r := range rs {
		b.WriteString(code[cursor:r.start])
		b.WriteString(r.text)
		cursor = r.end
	}
	b.WriteString(code[cursor:])
	return b.String()
}

// writeRewritten writes the code region ts.text[start:end] of a fragment body
// with its rewrites applied, giving every piece its own source position so
// that columns after a substitution stay exact.
func (x *expansion) writeRewritten(w *writer, ts *textSource, start, end int, scope *fragmentScope) {
	code := string(ts.text[start:end])
	cursor := 0
	for _, r := range codeRewrites(code, scope.subst) {
		w.write(code[cursor:r.start], ts.pos(start+cursor), SegFragment, scope.name)
		w.write(r.text, ts.pos(start+r.start), SegFragment, scope.name)
		cursor = r.end
	}
	w.write(code[cursor:], ts.pos(start+cursor), SegFragment, scope.name)
}

// dedupeDiagnostics sorts diagnostics and drops exact repeats, which a
// derived statement produces when it re-expands text its source statement
// already reported on.
func dedupeDiagnostics(ds Diagnostics) Diagnostics {
	sortDiagnostics(ds)
	out := ds[:0]
	for i, d := range ds {
		if i > 0 && d == ds[i-1] {
			continue
		}
		out = append(out, d)
	}
	return out
}

// writer accumulates output text and the source map segments that describe
// where each piece came from.
type writer struct {
	buf  strings.Builder
	line int // current output line, 1-based
	col  int // next output column, 1-based
	segs []Segment
	stmt string // derived statement the text belongs to, recorded in every segment
	// splices are the fragment expansions in progress, innermost last. Inside
	// one, every line after a line break is indented to the column of the
	// directive, so a multi-line body lines up under its directive instead of
	// keeping the indentation of the fragment file.
	splices []splice
	pending bool // a line break was written inside a splice; indent the next non-blank line
}

// splice records where a fragment body is being written.
type splice struct {
	indent int    // columns to insert before each continuation line
	src    Pos    // the directive, which the inserted spaces map to
	frag   string // fragment name
}

func newWriter() *writer {
	return &writer{line: 1, col: 1}
}

func (w *writer) String() string { return w.buf.String() }

// beginSplice starts a fragment expansion at the current column; endSplice
// ends the innermost one. A directive at the start of a line inside another
// fragment's body gets that body's indentation first, so the nested body
// aligns under the nested directive's real column.
func (w *writer) beginSplice(src Pos, frag string) {
	w.flushPending()
	w.splices = append(w.splices, splice{indent: w.col - 1, src: src, frag: frag})
}

func (w *writer) endSplice() {
	w.splices = w.splices[:len(w.splices)-1]
	if len(w.splices) == 0 {
		w.pending = false
	}
}

// flushPending writes the indentation a line break inside a splice asked
// for, as a generated segment that maps to the directive.
func (w *writer) flushPending() {
	if !w.pending {
		return
	}
	w.pending = false
	sp := w.splices[len(w.splices)-1]
	if sp.indent > 0 {
		w.segs = append(w.segs, Segment{OutLine: w.line, OutCol: w.col, Src: sp.src, Kind: SegGenerated, Fragment: sp.frag, Derived: w.stmt})
		w.advance(strings.Repeat(" ", sp.indent))
	}
}

func (w *writer) indent() int {
	if n := len(w.splices); n > 0 {
		return w.splices[n-1].indent
	}
	return 0
}

// write appends text that starts at src, attributed to kind and frag. Inside
// a splice with a non-zero indent, each line of text becomes its own segment
// and the indentation is written as a generated segment that maps to the
// directive, so every segment still copies its source contiguously.
func (w *writer) write(text string, src Pos, kind SegmentKind, frag string) {
	for text != "" {
		if text[0] == '\n' { // blank lines stay blank
			w.pending = false
		}
		w.flushPending()
		w.segs = append(w.segs, Segment{OutLine: w.line, OutCol: w.col, Src: src, Kind: kind, Fragment: frag, Derived: w.stmt})
		i := strings.IndexByte(text, '\n')
		if w.indent() == 0 || i < 0 {
			w.advance(text)
			return
		}
		w.advance(text[:i+1])
		w.pending = true
		text = text[i+1:]
		src = Pos{File: src.File, Line: src.Line + 1, Col: 1}
	}
}

func (w *writer) advance(text string) {
	w.buf.WriteString(text)
	if n := strings.Count(text, "\n"); n > 0 {
		w.line += n
		w.col = len(text) - strings.LastIndexByte(text, '\n')
	} else {
		w.col += len(text)
	}
}

// append appends another writer's output, shifting its segments to the
// current position.
func (w *writer) append(o *writer) {
	if o.buf.Len() == 0 {
		return
	}
	for _, s := range o.segs {
		if s.OutLine == 1 {
			s.OutCol += w.col - 1
		}
		s.OutLine += w.line - 1
		w.segs = append(w.segs, s)
	}
	w.advance(o.String())
}

// segOffsets returns the byte offset in w's text at which each segment
// starts.
func (w *writer) segOffsets() []int {
	li := newLineIndex([]byte(w.String()))
	offs := make([]int, len(w.segs))
	for i, s := range w.segs {
		offs[i] = li.starts[s.OutLine-1] + s.OutCol - 1
	}
	return offs
}
