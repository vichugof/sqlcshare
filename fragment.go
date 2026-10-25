package sqlcshare

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Fragment is one named piece of SQL.
type Fragment struct {
	Name   string
	Params []string // identifier parameters substituted for {param} tokens
	Kind   Kind
	Body   string // text between the header line and the terminating ';', trimmed
	// Pos is the position of the header comment and BodyPos the position of
	// the first byte of Body in the fragment file.
	Pos     Pos
	BodyPos Pos
}

// Library holds fragments by name in definition order.
type Library struct {
	frags map[string]*Fragment
	order []string
}

// NewLibrary returns an empty library.
func NewLibrary() *Library {
	return &Library{frags: map[string]*Fragment{}}
}

// Add adds a fragment. It replaces a fragment of the same name; Load reports
// duplicates before calling it.
func (l *Library) Add(f *Fragment) {
	if _, ok := l.frags[f.Name]; !ok {
		l.order = append(l.order, f.Name)
	}
	l.frags[f.Name] = f
}

// Get returns the fragment with the given name. A nil library is empty.
func (l *Library) Get(name string) (*Fragment, bool) {
	if l == nil {
		return nil, false
	}
	f, ok := l.frags[name]
	return f, ok
}

// Fragments returns the fragments in definition order.
func (l *Library) Fragments() []*Fragment {
	if l == nil {
		return nil
	}
	out := make([]*Fragment, 0, len(l.order))
	for _, name := range l.order {
		out = append(out, l.frags[name])
	}
	return out
}

// Load reads every fragment file matched by patterns from fsys into a new
// library. A pattern is a file, a directory (its .sql files, one level deep,
// hidden and .down.sql files skipped, as sqlc lists a queries directory), or
// a glob. The returned diagnostics include every problem found; the library
// is complete when none has severity Error.
func Load(engine Engine, fsys fs.FS, patterns ...string) (*Library, Diagnostics) {
	lib := NewLibrary()
	var diags Diagnostics
	files, err := resolveSQLFiles(fsys, patterns)
	if err != nil {
		diags.add(Pos{}, Error, "fragments: %v", err)
		return lib, diags
	}
	for _, name := range files {
		src, err := fs.ReadFile(fsys, name)
		if err != nil {
			diags.add(Pos{File: name}, Error, "%v", err)
			continue
		}
		frags, ds := ParseFragments(engine, name, src)
		diags = append(diags, ds...)
		for _, f := range frags {
			if prev, dup := lib.Get(f.Name); dup {
				diags.add(f.Pos, Error, "fragment %q is already defined at %s", f.Name, prev.Pos)
				continue
			}
			lib.Add(f)
		}
	}
	sortDiagnostics(diags)
	return lib, diags
}

var (
	fragmentHeaderRE = regexp.MustCompile(`^(?:--|#)\s*fragment:\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:\(([^)]*)\))?\s*:([A-Za-z]+)\s*$`)
	placeholderRE    = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	// atParamRE matches sqlc's @name parameter shorthand. The leading class
	// excludes operator characters so that PostgreSQL's @>, <@ and @@ and
	// MySQL's @@variables are left alone.
	atParamRE = regexp.MustCompile(`(^|[^A-Za-z0-9_@:$])@([A-Za-z_][A-Za-z0-9_]*)`)
	// macroRE matches the sqlc parameter macros, with or without quotes
	// around the name.
	macroRE       = regexp.MustCompile(`\bsqlc\.(arg|narg|slice)\(\s*'?([A-Za-z_][A-Za-z0-9_]*)'?\s*\)`)
	dollarParamRE = regexp.MustCompile(`\$[0-9]+`)
)

// ParseFragments parses the fragment definitions in one file. Each definition
// is a header comment followed by a body that ends at the next semicolon:
//
//	-- fragment: visible_author(a) :where
//	{a}.deleted_at IS NULL AND {a}.org_id = sqlc.arg(org_id);
//
// When a body is expanded, @name is rewritten to sqlc.arg(name) so that
// fragments are portable to MySQL, where @name is a user variable.
func ParseFragments(engine Engine, file string, src []byte) ([]*Fragment, Diagnostics) {
	var (
		frags     []*Fragment
		diags     Diagnostics
		cur       *Fragment
		bodyStart int
	)
	li := newLineIndex(src)
	pos := func(off int) Pos {
		line, col := li.lineCol(off)
		return Pos{File: file, Line: line, Col: col}
	}
	outside := func(off int) {
		diags.add(pos(off), Error, "SQL outside a fragment definition; add a '-- fragment: name(params) :kind' header")
	}
	for _, t := range scanSQL(engine, src) {
		switch t.kind {
		case tokLineComment:
			m := fragmentHeaderRE.FindSubmatch(src[t.start:t.end])
			if m == nil {
				continue
			}
			if cur != nil {
				diags.add(cur.Pos, Error, "fragment %q: missing terminating ';'", cur.Name)
			}
			cur = parseFragmentHeader(m, pos(t.start), &diags)
			bodyStart = lineEndInclusive(src, t.end)
		case tokSemicolon:
			if cur == nil {
				diags.add(pos(t.start), Error, "';' outside a fragment definition")
				continue
			}
			raw := src[bodyStart:t.start]
			lead := len(raw) - len(bytes.TrimLeft(raw, " \t\r\n\f\v"))
			body := strings.TrimSpace(string(raw))
			if body == "" {
				diags.add(cur.Pos, Error, "fragment %q has an empty body", cur.Name)
				cur = nil
				continue
			}
			cur.BodyPos = pos(bodyStart + lead)
			cur.Body = body
			diags = append(diags, validateFragmentBody(engine, cur)...)
			frags = append(frags, cur)
			cur = nil
		case tokCode:
			if cur == nil {
				if k := firstNonSpace(src, t.start, t.end); k >= 0 {
					outside(k)
				}
			}
		case tokString, tokQuoted, tokDollar:
			if cur == nil {
				outside(t.start)
			}
		}
	}
	if cur != nil {
		diags.add(cur.Pos, Error, "fragment %q: missing terminating ';'", cur.Name)
	}
	return frags, diags
}

// parseFragmentHeader builds a fragment from a header match. It returns nil
// after recording a diagnostic when the header is invalid.
func parseFragmentHeader(m [][]byte, at Pos, diags *Diagnostics) *Fragment {
	f := &Fragment{Name: string(m[1]), Pos: at}
	params, err := parseIdentList(string(m[2]))
	if err != nil {
		diags.add(at, Error, "fragment %q: %v", f.Name, err)
		return nil
	}
	for i, p := range params {
		if slices.Contains(params[:i], p) {
			diags.add(at, Error, "fragment %q: duplicate parameter %q", f.Name, p)
			return nil
		}
	}
	f.Params = params
	kind, ok := ParseKind(strings.ToLower(string(m[3])))
	if !ok {
		diags.add(at, Error, "fragment %q: unknown kind %q (want columns, where, joins, cte or sql)", f.Name, m[3])
		return nil
	}
	f.Kind = kind
	return f
}

// validateFragmentBody checks the rules that make a fragment safe to splice
// anywhere: every placeholder is a declared parameter, and parameters are
// named so that sqlc numbers them after expansion.
func validateFragmentBody(engine Engine, f *Fragment) Diagnostics {
	var diags Diagnostics
	ts := newTextSource(f.Pos.File, []byte(f.Body), f.BodyPos)
	toks := scanSQL(engine, ts.text)
	if engine == PostgreSQL {
		for _, m := range findMacros(toks, f.Body) {
			if m.form == "slice" {
				diags.add(ts.pos(m.start), Error, "fragment %q: sqlc.slice(%s) compiles to IN ($n) with a slice argument, which fails at runtime on PostgreSQL; use = ANY(sqlc.arg(%s)::type[])", f.Name, m.name, m.name)
			}
		}
	}
	for _, t := range toks {
		text := f.Body[t.start:t.end]
		isCode := t.kind == tokCode
		if !isCode && !(t.kind == tokBlockComment && isDirectiveComment(text)) {
			for _, m := range placeholderRE.FindAllStringSubmatchIndex(text, -1) {
				if name := text[m[2]:m[3]]; slices.Contains(f.Params, name) {
					diags.add(ts.pos(t.start+m[0]), Warning, "fragment %q: {%s} inside a literal or comment is not substituted", f.Name, name)
				}
			}
			continue
		}
		for _, m := range placeholderRE.FindAllStringSubmatchIndex(text, -1) {
			if name := text[m[2]:m[3]]; !slices.Contains(f.Params, name) {
				diags.add(ts.pos(t.start+m[0]), Error, "fragment %q: {%s} is not a declared parameter", f.Name, name)
			}
		}
		if !isCode {
			continue
		}
		for _, m := range dollarParamRE.FindAllStringIndex(text, -1) {
			diags.add(ts.pos(t.start+m[0]), Error, "fragment %q: positional parameter %s; use sqlc.arg(name) so that sqlc numbers it after expansion", f.Name, text[m[0]:m[1]])
		}
		if engine == SQLite {
			for i := strings.IndexByte(text, '?'); i >= 0; i = strings.IndexByte(text, '?') {
				diags.add(ts.pos(t.start+i), Error, "fragment %q: unnumbered ? binds by position on SQLite; use sqlc.arg(name)", f.Name)
				text = text[i+1:]
				t.start += i + 1
			}
		}
	}
	return diags
}

// resolveSQLFiles expands patterns to a de-duplicated list of files, in
// pattern order and sorted within each pattern. A pattern is a file, a
// directory, or a glob in which a "**" segment matches any number of
// directories.
func resolveSQLFiles(fsys fs.FS, patterns []string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	for _, p := range patterns {
		p = path.Clean(strings.ReplaceAll(p, "\\", "/"))
		matches := []string{p}
		if strings.ContainsAny(p, "*?[") {
			var err error
			if strings.Contains(p, "**") {
				matches, err = globStar(fsys, p)
			} else {
				matches, err = fs.Glob(fsys, p)
			}
			if err != nil {
				return nil, err
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("%s matches no file or directory", p)
			}
			sort.Strings(matches)
		}
		var found []string
		for _, m := range matches {
			info, err := fs.Stat(fsys, m)
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("%s does not exist", m)
			}
			if err != nil {
				return nil, err
			}
			if !info.IsDir() {
				found = append(found, m)
				continue
			}
			entries, err := fs.ReadDir(fsys, m)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if !e.IsDir() && isQueryFile(e.Name()) {
					found = append(found, path.Join(m, e.Name()))
				}
			}
		}
		sort.Strings(found)
		for _, name := range found {
			if !seen[name] {
				seen[name] = true
				files = append(files, name)
			}
		}
	}
	return files, nil
}

// globStar matches a pattern in which a "**" segment stands for zero or more
// directories (hidden ones excluded); the other segments follow path.Match.
// A trailing "**" lists every query file below the directory, at any depth.
// fs.Glob has no such segment, and sqlc lists a queries directory one level
// deep, so this is how a tree of query directories such as adapter/*/query
// and adapter/*/*/query is named once.
func globStar(fsys fs.FS, pattern string) ([]string, error) {
	if _, err := path.Match(strings.ReplaceAll(pattern, "**", "*"), ""); err != nil {
		return nil, fmt.Errorf("%s: %w", pattern, err)
	}
	var out []string
	var walk func(dir string, segs []string) error
	walk = func(dir string, segs []string) error {
		if len(segs) == 0 {
			out = append(out, dir)
			return nil
		}
		name := dir
		if name == "" {
			name = "."
		}
		entries, err := fs.ReadDir(fsys, name)
		if err != nil {
			return err
		}
		if segs[0] == "**" {
			if len(segs) > 1 { // zero directories, then one more
				if err := walk(dir, segs[1:]); err != nil {
					return err
				}
			}
			for _, e := range entries {
				child := path.Join(dir, e.Name())
				switch {
				case e.IsDir() && !strings.HasPrefix(e.Name(), "."):
					if err := walk(child, segs); err != nil {
						return err
					}
				case len(segs) == 1 && !e.IsDir() && isQueryFile(e.Name()):
					out = append(out, child) // a trailing ** is a recursive directory listing
				}
			}
			return nil
		}
		for _, e := range entries {
			if ok, _ := path.Match(segs[0], e.Name()); !ok {
				continue
			}
			child := path.Join(dir, e.Name())
			if len(segs) == 1 {
				out = append(out, child)
			} else if e.IsDir() {
				if err := walk(child, segs[1:]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	err := walk("", strings.Split(pattern, "/"))
	sort.Strings(out)
	return out, err
}

// isQueryFile applies sqlc's rule for listing a queries directory.
func isQueryFile(name string) bool {
	return strings.HasSuffix(name, ".sql") && !strings.HasPrefix(name, ".") && !strings.HasSuffix(name, ".down.sql")
}
