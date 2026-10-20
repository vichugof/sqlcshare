package sqlcshare

import (
	"fmt"
	"sort"
	"strings"
)

// The lint checks look at an expanded statement for the parameter mistakes
// that sqlc accepts but that fail at compile time or at runtime, as found in
// the Phase 0 spike. They need only the expanded text and the source map.

type paramRef struct {
	off  int
	name string // empty for positional placeholders
	form string // "arg", "narg", "slice" or "positional"
	line int
	col  int
}

func (r paramRef) String() string {
	if r.form == "positional" {
		return "a positional placeholder"
	}
	return fmt.Sprintf("sqlc.%s(%s)", r.form, r.name)
}

// findParams locates the parameter references in the code regions of text.
func findParams(engine Engine, text string) []paramRef {
	var refs []paramRef
	src := []byte(text)
	li := newLineIndex(src)
	add := func(off int, name, form string) {
		line, col := li.lineCol(off)
		refs = append(refs, paramRef{off: off, name: name, form: form, line: line, col: col})
	}
	toks := scanSQL(engine, src)
	for _, m := range findMacros(toks, text) {
		add(m.start, m.name, m.form)
	}
	for _, t := range toks {
		if t.kind != tokCode {
			continue
		}
		code := text[t.start:t.end]
		switch engine {
		case PostgreSQL, SQLite:
			for _, m := range atParamRE.FindAllStringSubmatchIndex(code, -1) {
				add(t.start+m[4]-1, code[m[4]:m[5]], "arg")
			}
		}
		switch engine {
		case PostgreSQL:
			for _, m := range dollarParamRE.FindAllStringIndex(code, -1) {
				add(t.start+m[0], "", "positional")
			}
		case MySQL, SQLite:
			for i := 0; i < len(code); i++ {
				if code[i] == '?' {
					add(t.start+i, "", "positional")
				}
			}
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].off < refs[j].off })
	return refs
}

// lint runs the checks on one expanded statement body.
func (x *expansion) lint(bw *writer, stmt *Statement) {
	refs := findParams(x.e.Engine, bw.String())
	if len(refs) == 0 {
		return
	}
	sm := &SourceMap{Segments: bw.segs}
	origin := func(r paramRef) (Pos, string) {
		p, seg, ok := sm.Origin(r.line, r.col)
		if !ok {
			return stmt.Pos, ""
		}
		if seg.Kind == SegFragment {
			return p, seg.Fragment
		}
		return p, ""
	}
	switch x.e.Engine {
	case SQLite:
		for i, r := range refs {
			if r.form == "slice" && i < len(refs)-1 {
				p, _ := origin(r)
				x.diags.add(p, Error, "sqlc.slice(%s) must be the last parameter of the statement on SQLite: %s follows it and would bind to the wrong index (sqlc issue #2452)", r.name, refs[i+1])
			}
		}
	case PostgreSQL:
		for _, r := range refs {
			if r.form != "slice" {
				continue
			}
			if p, frag := origin(r); frag == "" {
				x.diags.add(p, Warning, "sqlc.slice(%s) compiles to IN ($n) with a slice argument, which fails at runtime on PostgreSQL; use = ANY(sqlc.arg(%s)::type[])", r.name, r.name)
			}
		}
	}
	var names []string
	byName := map[string][]int{}
	for i, r := range refs {
		if r.name == "" {
			continue
		}
		if _, seen := byName[r.name]; !seen {
			names = append(names, r.name)
		}
		byName[r.name] = append(byName[r.name], i)
	}
	limit := x.e.ParamLimit
	if limit == 0 {
		limit = 1
	}
	for _, name := range names {
		idxs := byName[name]
		if len(idxs) < 2 {
			continue
		}
		if x.e.Engine == MySQL && limit > 1 {
			p, _ := origin(refs[idxs[1]])
			x.diags.add(p, Error, "parameter %q appears %d times: sqlc generates duplicate Go parameters for MySQL when query_parameter_limit is greater than 1 (sqlc issue #4191)", name, len(idxs))
		}
		var sites []string
		for _, i := range idxs {
			_, frag := origin(refs[i])
			site := "the query"
			if frag != "" {
				site = "fragment " + frag
			}
			if !contains(sites, site) {
				sites = append(sites, site)
			}
		}
		if len(sites) > 1 {
			p, _ := origin(refs[idxs[0]])
			x.diags.add(p, Info, "parameter %q is bound by %s; sqlc types it from its first use on PostgreSQL and SQLite and reports conflicting types only on MySQL", name, strings.Join(sites, " and "))
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
