package sqlcshare

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestParseFragments(t *testing.T) {
	src := `-- a comment before the first fragment is fine

-- fragment: cols(a) :columns
{a}.id, {a}.name;

--fragment: visible( a , b ) :WHERE
  -- a comment inside the body stays in the body
  {a}.deleted_at IS NULL AND {a}.org_id = @org_id AND {b}.x @> '{a}' ;

# fragment: recent :cte
SELECT 1;
`
	frags, diags := ParseFragments(MySQL, "f.sql", []byte(src))
	if len(diags) != 1 || diags[0].Severity != Warning || !strings.Contains(diags[0].Message, "{a} inside a literal") {
		t.Fatalf("want only the warning about '{a}' in the string literal, got:\n%v", diags)
	}
	if len(frags) != 3 {
		t.Fatalf("got %d fragments, want 3", len(frags))
	}
	cols := frags[0]
	if cols.Name != "cols" || cols.Kind != KindColumns || strings.Join(cols.Params, ",") != "a" || cols.Body != "{a}.id, {a}.name" {
		t.Errorf("cols: %+v", cols)
	}
	if cols.Pos != (Pos{File: "f.sql", Line: 3, Col: 1}) || cols.BodyPos != (Pos{File: "f.sql", Line: 4, Col: 1}) {
		t.Errorf("cols positions: %v %v", cols.Pos, cols.BodyPos)
	}
	vis := frags[1]
	if vis.Kind != KindWhere || strings.Join(vis.Params, ",") != "a,b" {
		t.Errorf("visible: %+v", vis)
	}
	wantBody := "-- a comment inside the body stays in the body\n  {a}.deleted_at IS NULL AND {a}.org_id = @org_id AND {b}.x @> '{a}'"
	if vis.Body != wantBody {
		t.Errorf("visible body:\n got %q\nwant %q", vis.Body, wantBody)
	}
	if vis.BodyPos != (Pos{File: "f.sql", Line: 7, Col: 3}) {
		t.Errorf("visible body position: %v", vis.BodyPos)
	}
	if frags[2].Name != "recent" || frags[2].Kind != KindCTE || len(frags[2].Params) != 0 {
		t.Errorf("recent: %+v", frags[2])
	}
}

func TestParseFragmentsErrors(t *testing.T) {
	tests := []struct {
		name   string
		engine Engine
		src    string
		want   string // substring of the first diagnostic
	}{
		{"missing semicolon", PostgreSQL, "-- fragment: a :sql\nSELECT 1\n", `fragment "a": missing terminating ';'`},
		{"header inside a body", PostgreSQL, "-- fragment: a :sql\nSELECT 1\n-- fragment: b :sql\nSELECT 2;", `fragment "a": missing terminating ';'`},
		{"unknown kind", PostgreSQL, "-- fragment: a :partial\nx;", `unknown kind "partial"`},
		{"duplicate parameter", PostgreSQL, "-- fragment: a(x, x) :sql\nx;", `duplicate parameter "x"`},
		{"bad parameter", PostgreSQL, "-- fragment: a(1x) :sql\nx;", `"1x" is not an identifier`},
		{"undeclared placeholder", PostgreSQL, "-- fragment: a(x) :where\n{x}.id = {y}.id;", `{y} is not a declared parameter`},
		{"placeholder in nested directive", PostgreSQL, "-- fragment: a(x) :where\n/*%where b({z})*/ TRUE /*%end*/;", `{z} is not a declared parameter`},
		{"positional parameter", PostgreSQL, "-- fragment: a :where\nid = $1;", `positional parameter $1`},
		{"question mark on sqlite", SQLite, "-- fragment: a :where\nid = ?;", `unnumbered ? binds by position on SQLite`},
		{"slice on postgresql", PostgreSQL, "-- fragment: a :where\nid IN (sqlc.slice(ids));", `sqlc.slice(ids) compiles to IN ($n)`},
		{"empty body", PostgreSQL, "-- fragment: a :where\n  ;", `fragment "a" has an empty body`},
		{"sql outside", PostgreSQL, "SELECT 1;\n-- fragment: a :sql\nx;", `SQL outside a fragment definition`},
		{"semicolon outside", PostgreSQL, ";", `';' outside a fragment definition`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := ParseFragments(tt.engine, "f.sql", []byte(tt.src))
			if !diags.HasErrors() {
				t.Fatalf("expected an error containing %q, got none", tt.want)
			}
			if !strings.Contains(diags[0].Message, tt.want) {
				t.Errorf("first diagnostic %q does not contain %q", diags[0].Message, tt.want)
			}
		})
	}
	// Question marks are allowed in MySQL fragments (LIMIT ? has no macro
	// form there) and are operators on PostgreSQL.
	for _, engine := range []Engine{MySQL, PostgreSQL} {
		if _, diags := ParseFragments(engine, "f.sql", []byte("-- fragment: a :sql\nLIMIT ?;")); diags.HasErrors() {
			t.Errorf("%s: ? in a fragment should be allowed: %v", engine, diags)
		}
	}
}

func TestCodeRewrites(t *testing.T) {
	subst := map[string]string{"a": "x"}
	tests := []struct{ in, want string }{
		{"{a}.id = @x", "x.id = sqlc.arg(x)"},
		{"a = @x::int AND b = @y", "a = sqlc.arg(x)::int AND b = sqlc.arg(y)"},
		{"tags @> ARRAY[1] AND @>", "tags @> ARRAY[1] AND @>"},
		{"@@session.var", "@@session.var"},
		{"{b}.id AND {a}", "{b}.id AND x"},
		{"plain", "plain"},
	}
	for _, tt := range tests {
		if got := applyRewrites(tt.in, codeRewrites(tt.in, subst)); got != tt.want {
			t.Errorf("%q: got %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLoad(t *testing.T) {
	fsys := fstest.MapFS{
		"fragments/a.sql":        {Data: []byte("-- fragment: one :sql\n1;")},
		"fragments/b.sql":        {Data: []byte("-- fragment: two :sql\n2;")},
		"fragments/.hidden.sql":  {Data: []byte("-- fragment: hidden :sql\n3;")},
		"fragments/x.down.sql":   {Data: []byte("-- fragment: down :sql\n4;")},
		"fragments/notes.txt":    {Data: []byte("not sql")},
		"fragments/sub/c.sql":    {Data: []byte("-- fragment: nested :sql\n5;")},
		"extra/dup.sql":          {Data: []byte("-- fragment: one :sql\n1 again;")},
		"extra/glob1.sql":        {Data: []byte("-- fragment: g1 :sql\n6;")},
		"extra/glob2.sql":        {Data: []byte("-- fragment: g2 :sql\n7;")},
		"extra/glob3.txt":        {Data: []byte("-- fragment: g3 :sql\n8;")},
		"single/explicit.custom": {Data: []byte("-- fragment: explicit :sql\n9;")},
	}
	lib, diags := Load(PostgreSQL, fsys, "fragments", "extra/glob*.sql", "single/explicit.custom")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics:\n%v", diags)
	}
	var names []string
	for _, f := range lib.Fragments() {
		names = append(names, f.Name)
	}
	if got, want := strings.Join(names, ","), "one,two,g1,g2,explicit"; got != want {
		t.Errorf("fragments loaded: got %s, want %s", got, want)
	}
	if _, diags := Load(PostgreSQL, fsys, "fragments", "extra/dup.sql"); !diags.HasErrors() || diags[0].Pos.String() != "extra/dup.sql:1:1" || !strings.Contains(diags[0].Message, `fragment "one" is already defined at fragments/a.sql:1:1`) {
		t.Errorf("duplicate across files: %v", diags)
	}
	if _, diags := Load(PostgreSQL, fsys, "missing"); !diags.HasErrors() || diags[0].Message != "fragments: missing does not exist" {
		t.Errorf("missing path should be an error: %v", diags)
	}
	if _, diags := Load(PostgreSQL, fsys, "extra/none*.sql"); !diags.HasErrors() || diags[0].Message != "fragments: extra/none*.sql matches no file or directory" {
		t.Errorf("glob without matches should be an error: %v", diags)
	}
	// A ** segment matches any number of directories; like fs.Glob, it does
	// not apply the directory-listing rule, so hidden and .down files match.
	lib, diags = Load(PostgreSQL, fsys, "fragments/**/*.sql")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics:\n%v", diags)
	}
	names = nil
	for _, f := range lib.Fragments() {
		names = append(names, f.Name)
	}
	if got, want := strings.Join(names, ","), "hidden,one,two,nested,down"; got != want {
		t.Errorf("** glob: got %s, want %s", got, want)
	}
	if files, err := resolveSQLFiles(fsys, []string{"**/glob?.sql", "extra/**"}); err != nil || strings.Join(files, ",") != "extra/glob1.sql,extra/glob2.sql,extra/dup.sql" {
		t.Errorf("** globs: %v %v", files, err)
	}
	if _, err := resolveSQLFiles(fsys, []string{"**/[.sql"}); err == nil {
		t.Error("bad ** pattern should be an error")
	}
}
