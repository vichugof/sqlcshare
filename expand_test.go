package sqlcshare

import (
	"strings"
	"testing"
)

const testFragments = `-- fragment: cols(a) :columns
{a}.id, {a}.name;

-- fragment: visible(a) :where
{a}.deleted_at IS NULL AND {a}.org_id = sqlc.arg(org_id);

-- fragment: org_join(a, o) :joins
JOIN orgs {o} ON {o}.id = {a}.org_id;

-- fragment: recent :cte
SELECT o.* FROM orders o
WHERE o.created_at > sqlc.narg(since)::timestamptz;

-- fragment: by_id :sql
ORDER BY id;

-- fragment: visible_named(a) :where
/*%where visible({a})*/ TRUE /*%end*/ AND {a}.name = @name;

-- fragment: loop_a :sql
/*%sql loop_b*/;

-- fragment: literal(a) :where
{a}.email <> 'me@example.com' AND {a}.note <> '{a}' AND {a}.tags @> '{x}';

-- fragment: loop_b :sql
/*%sql loop_a*/;
`

func mustLib(t *testing.T, engine Engine, src string) *Library {
	t.Helper()
	frags, diags := ParseFragments(engine, "frags.sql", []byte(src))
	if diags.HasErrors() {
		t.Fatalf("fragments:\n%v", diags)
	}
	lib := NewLibrary()
	for _, f := range frags {
		lib.Add(f)
	}
	return lib
}

func TestExpand(t *testing.T) {
	lib := mustLib(t, PostgreSQL, testFragments)
	tests := []struct {
		name string
		in   string
		want string
		uses string // "kind name, kind name" of the first statement
	}{
		{
			name: "where with fallback",
			in:   "-- name: CountAuthors :one\nSELECT count(*) FROM authors a\nWHERE /*%where visible(a)*/ TRUE /*%end*/;\n",
			want: "-- name: CountAuthors :one\n-- sqlcshare: q.sql:1 (where visible)\nSELECT count(*) FROM authors a\nWHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id));\n",
			uses: "where visible",
		},
		{
			name: "columns in place",
			in:   "-- name: List :many\nSELECT /*%columns cols(a)*/ FROM authors a;",
			want: "-- name: List :many\n-- sqlcshare: q.sql:1 (columns cols)\nSELECT a.id, a.name FROM authors a;",
			uses: "columns cols",
		},
		{
			name: "joins and raw sql",
			in:   "-- name: List :many\nSELECT a.id FROM authors a /*%joins org_join(a, o)*/ /*%sql by_id*/ LIMIT 1 /*%end*/;",
			want: "-- name: List :many\n-- sqlcshare: q.sql:1 (joins org_join, sql by_id)\nSELECT a.id FROM authors a JOIN orgs o ON o.id = a.org_id ORDER BY id;",
			uses: "joins org_join, sql by_id",
		},
		{
			name: "cte inside WITH, multi-line body",
			in:   "-- name: X :many\nWITH /*%cte recent*/ recent AS (SELECT 1) /*%end*/\nSELECT count(*) FROM recent;",
			want: "-- name: X :many\n-- sqlcshare: q.sql:1 (cte recent)\nWITH recent AS (SELECT o.* FROM orders o\nWHERE o.created_at > sqlc.narg(since)::timestamptz)\nSELECT count(*) FROM recent;",
			uses: "cte recent",
		},
		{
			name: "nested fragment and @name normalization",
			in:   "-- name: X :many\nSELECT 1 FROM authors a WHERE /*%where visible_named(a)*/ TRUE /*%end*/;",
			want: "-- name: X :many\n-- sqlcshare: q.sql:1 (where visible_named)\nSELECT 1 FROM authors a WHERE ((a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id)) AND a.name = sqlc.arg(name));",
			uses: "where visible_named",
		},
		{
			name: "two directives on one line and a comment in between",
			in:   "-- name: X :many\nSELECT /*%columns cols(a)*/ a.* /* keep */ /*%end*/ FROM authors a WHERE /*%where visible(a)*/ TRUE /*%end*/;",
			want: "-- name: X :many\n-- sqlcshare: q.sql:1 (columns cols, where visible)\nSELECT a.id, a.name FROM authors a WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id));",
			uses: "columns cols, where visible",
		},
		{
			name: "no name comment means no provenance",
			in:   "SELECT 1 FROM authors a WHERE /*%where visible(a)*/ TRUE /*%end*/;",
			want: "SELECT 1 FROM authors a WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id));",
			uses: "where visible",
		},
		{
			name: "comments between name line and body are kept after the provenance",
			in:   "-- name: X :many\n-- doc\n\nSELECT /*%columns cols(a)*/ FROM authors a;",
			want: "-- name: X :many\n-- sqlcshare: q.sql:1 (columns cols)\n-- doc\n\nSELECT a.id, a.name FROM authors a;",
			uses: "columns cols",
		},
		{
			name: "strings inside fragments are not rewritten",
			in:   "SELECT 1 FROM users u WHERE /*%where literal(u)*/ TRUE /*%end*/;",
			want: "SELECT 1 FROM users u WHERE (u.email <> 'me@example.com' AND u.note <> '{a}' AND u.tags @> '{x}');",
			uses: "where literal",
		},
		{
			name: "directives inside strings and ordinary comments are ignored",
			in:   "-- name: X :one\nSELECT '/*%where visible(a)*/' /* not %where */ FROM t;",
			want: "-- name: X :one\nSELECT '/*%where visible(a)*/' /* not %where */ FROM t;",
			uses: "",
		},
		{
			name: "two statements with their own provenance lines",
			in:   "-- name: A :one\nSELECT /*%columns cols(a)*/ FROM authors a;\n\n-- name: B :one\nSELECT count(*) FROM authors a WHERE /*%where visible(a)*/ TRUE /*%end*/;\n",
			want: "-- name: A :one\n-- sqlcshare: q.sql:1 (columns cols)\nSELECT a.id, a.name FROM authors a;\n\n-- name: B :one\n-- sqlcshare: q.sql:4 (where visible)\nSELECT count(*) FROM authors a WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id));\n",
			uses: "columns cols",
		},
		{
			name: "trailing comment after the last statement",
			in:   "-- name: A :one\nSELECT 1;\n-- the end\n",
			want: "-- name: A :one\nSELECT 1;\n-- the end\n",
			uses: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := &Expander{Engine: PostgreSQL, Lib: lib}
			res, err := ex.Expand("q.sql", []byte(tt.in))
			if err != nil {
				t.Fatalf("unexpected error:\n%v", err)
			}
			if got := string(res.Text); got != tt.want {
				t.Errorf("output:\n--- got\n%s\n--- want\n%s", got, tt.want)
			}
			if len(res.Statements) == 0 {
				t.Fatal("no statements recorded")
			}
			var uses []string
			for _, u := range res.Statements[0].DirectUses() {
				uses = append(uses, u.Kind.String()+" "+u.Name)
			}
			if got := strings.Join(uses, ", "); got != tt.uses {
				t.Errorf("uses: got %q, want %q", got, tt.uses)
			}
		})
	}
}

func TestExpandOptions(t *testing.T) {
	lib := mustLib(t, PostgreSQL, testFragments)
	in := "-- name: X :one\nSELECT count(*) FROM authors a WHERE /*%where visible(a)*/ TRUE /*%end*/;\n\n-- name: Y :one\nSELECT 1;\n"
	ex := &Expander{Engine: PostgreSQL, Lib: lib, Marker: true, NoProvenance: true}
	res, err := ex.Expand("q.sql", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := "-- name: X :one\nSELECT /* sqlcshare */ count(*) FROM authors a WHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id));\n\n-- name: Y :one\nSELECT /* sqlcshare */ 1;\n"
	if got := string(res.Text); got != want {
		t.Errorf("marker and no provenance:\n--- got\n%s\n--- want\n%s", got, want)
	}
	if res.Statements[0].Name != "X" || res.Statements[1].Name != "Y" {
		t.Errorf("statement names: %+v", res.Statements)
	}
	if res.Statements[1].Pos != (Pos{File: "q.sql", Line: 5, Col: 1}) {
		t.Errorf("statement position: %v", res.Statements[1].Pos)
	}
	// A statement that does not start with a keyword cannot carry the marker.
	res, err = ex.Expand("q.sql", []byte("(SELECT 1) UNION (SELECT 2);"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Severity != Warning || !strings.Contains(res.Diagnostics[0].Message, "marker not inserted") {
		t.Errorf("expected a marker warning, got %v", res.Diagnostics)
	}
	if _, err := (&Expander{}).Expand("q.sql", nil); err == nil {
		t.Error("an unset engine must be rejected")
	}
}

func TestExpandErrors(t *testing.T) {
	lib := mustLib(t, PostgreSQL, testFragments)
	tests := []struct {
		name    string
		in      string
		wantErr string
		wantPos string
		wantOut string // output text when the directive is left in place; empty to skip
	}{
		{
			name:    "unknown fragment keeps the source text",
			in:      "-- name: X :one\nSELECT 1 FROM t WHERE /*%where nope(a)*/ TRUE /*%end*/;",
			wantErr: `unknown fragment "nope"`,
			wantPos: "q.sql:2:23",
			wantOut: "-- name: X :one\n-- sqlcshare: q.sql:1 (where nope)\nSELECT 1 FROM t WHERE /*%where nope(a)*/ TRUE /*%end*/;",
		},
		{
			name:    "kind mismatch",
			in:      "SELECT /*%columns visible(a)*/ FROM t;",
			wantErr: `fragment "visible" is defined as :where at frags.sql:4:1 but used as %columns`,
			wantPos: "q.sql:1:8",
		},
		{
			name:    "argument count",
			in:      "SELECT /*%columns cols(a, b)*/ FROM t;",
			wantErr: `fragment "cols" takes 1 argument(s) (a), got 2`,
			wantPos: "q.sql:1:8",
		},
		{
			name:    "cycle",
			in:      "SELECT 1 /*%sql loop_a*/;",
			wantErr: "fragment cycle: loop_a -> loop_b -> loop_a",
			wantPos: "frags.sql:27:1",
		},
		{
			name:    "end without directive",
			in:      "SELECT 1 /*%end*/;",
			wantErr: "/*%end*/ without a preceding directive",
			wantPos: "q.sql:1:10",
		},
		{
			name:    "malformed directive",
			in:      "SELECT 1 /*%where*/;",
			wantErr: "directive %where needs a fragment name",
			wantPos: "q.sql:1:10",
		},
		{
			name:    "unknown directive kind",
			in:      "SELECT 1 /*%include x*/;",
			wantErr: "unknown directive %include",
			wantPos: "q.sql:1:10",
		},
		{
			name:    "bad argument",
			in:      "SELECT 1 /*%where visible(a.b)*/;",
			wantErr: `"a.b" is not an identifier`,
			wantPos: "q.sql:1:10",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := &Expander{Engine: PostgreSQL, Lib: lib}
			res, err := ex.Expand("q.sql", []byte(tt.in))
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			diags, ok := err.(Diagnostics)
			if !ok {
				t.Fatalf("error is %T, want Diagnostics", err)
			}
			found := false
			for _, d := range diags {
				if strings.Contains(d.Message, tt.wantErr) {
					found = true
					if d.Pos.String() != tt.wantPos {
						t.Errorf("position: got %s, want %s", d.Pos, tt.wantPos)
					}
				}
			}
			if !found {
				t.Errorf("no diagnostic contains %q:\n%v", tt.wantErr, diags)
			}
			if tt.wantOut != "" && string(res.Text) != tt.wantOut {
				t.Errorf("partial output:\n--- got\n%s\n--- want\n%s", res.Text, tt.wantOut)
			}
		})
	}
}

func TestSourceMap(t *testing.T) {
	lib := mustLib(t, PostgreSQL, testFragments)
	ex := &Expander{Engine: PostgreSQL, Lib: lib}
	in := "-- name: X :many\nWITH /*%cte recent*/ recent AS (SELECT 1) /*%end*/\nSELECT count(*) FROM recent r\nWHERE /*%where visible(r)*/ TRUE /*%end*/ AND r.id > 0;\n"
	res, err := ex.Expand("q.sql", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := "-- name: X :many\n-- sqlcshare: q.sql:1 (cte recent, where visible)\nWITH recent AS (SELECT o.* FROM orders o\nWHERE o.created_at > sqlc.narg(since)::timestamptz)\nSELECT count(*) FROM recent r\nWHERE (r.deleted_at IS NULL AND r.org_id = sqlc.arg(org_id)) AND r.id > 0;\n"
	if got := string(res.Text); got != want {
		t.Fatalf("output:\n--- got\n%s\n--- want\n%s", got, want)
	}
	tests := []struct {
		line, col int
		want      string
		kind      SegmentKind
		fragment  string
	}{
		{1, 1, "q.sql:1:1", SegSource, ""},
		{2, 1, "q.sql:2:6", SegGenerated, ""},       // provenance maps to the first directive
		{3, 1, "q.sql:2:1", SegSource, ""},          // WITH
		{3, 6, "q.sql:2:6", SegGenerated, "recent"}, // "recent AS (" wrapper maps to the directive
		{3, 17, "frags.sql:11:1", SegFragment, "recent"},
		{3, 24, "frags.sql:11:8", SegFragment, "recent"},
		{4, 1, "frags.sql:12:1", SegFragment, "recent"},
		{4, 7, "frags.sql:12:7", SegFragment, "recent"},
		{5, 1, "q.sql:3:1", SegSource, ""}, // SELECT after the multi-line fragment
		{5, 8, "q.sql:3:8", SegSource, ""},
		{6, 7, "q.sql:4:7", SegGenerated, "visible"},
		{6, 8, "frags.sql:5:1", SegFragment, "visible"},   // the substituted {a}
		{6, 9, "frags.sql:5:4", SegFragment, "visible"},   // text after the substitution keeps its real column
		{6, 30, "frags.sql:5:25", SegFragment, "visible"}, // the N of AND
		{6, 44, "frags.sql:5:41", SegFragment, "visible"}, // sqlc.arg(org_id)
		{6, 63, "q.sql:4:44", SegSource, ""},              // the N of AND after the fallback
		{6, 0, "q.sql:4:1", SegSource, ""},                // column 0 is clamped to 1
	}
	for _, tt := range tests {
		p, seg, ok := res.Map.Origin(tt.line, tt.col)
		if !ok {
			t.Errorf("%d:%d: no origin", tt.line, tt.col)
			continue
		}
		if p.String() != tt.want || seg.Kind != tt.kind || seg.Fragment != tt.fragment {
			t.Errorf("%d:%d: got %s kind=%d fragment=%q, want %s kind=%d fragment=%q", tt.line, tt.col, p, seg.Kind, seg.Fragment, tt.want, tt.kind, tt.fragment)
		}
	}
	if _, _, ok := res.Map.Origin(0, 1); ok {
		t.Error("line 0 must have no origin")
	}
	res.Map.Out = "gen/sql/q.sql"
	maps := map[string]*SourceMap{"gen/sql/q.sql": res.Map}
	var out strings.Builder
	in2 := "# package db\ngen/sql/q.sql:6:8: column \"deleted_at\" does not exist\ngen/sql/q.sql:5:22: relation \"recent\" does not exist\nother/file.sql:1:1: untouched\ngen/sql/q.sql:6:8: column \"deleted_at\" does not exist\ngen/sql/q.sql:6:8: column \"deleted_at\" does not exist\n"
	if err := RewriteDiagnostics(strings.NewReader(in2), &out, maps); err != nil {
		t.Fatal(err)
	}
	wantOut := "# package db\nfrags.sql:5:1: column \"deleted_at\" does not exist (in fragment visible, expanded at gen/sql/q.sql:6:8 and 2 more)\nq.sql:3:22: relation \"recent\" does not exist (expanded at gen/sql/q.sql:5:22)\nother/file.sql:1:1: untouched\n"
	if out.String() != wantOut {
		t.Errorf("rewritten diagnostics:\n--- got\n%s\n--- want\n%s", out.String(), wantOut)
	}
}

func TestLint(t *testing.T) {
	t.Run("mysql repeated name with positional parameters", func(t *testing.T) {
		lib := mustLib(t, MySQL, "-- fragment: visible(a) :where\n{a}.deleted_at IS NULL AND {a}.org_id = sqlc.arg(org_id);")
		in := "-- name: X :many\nSELECT 1 FROM authors a WHERE /*%where visible(a)*/ TRUE /*%end*/ AND EXISTS (SELECT 1 FROM authors b WHERE /*%where visible(b)*/ TRUE /*%end*/);"
		ex := &Expander{Engine: MySQL, Lib: lib, ParamLimit: 10}
		res, err := ex.Expand("q.sql", []byte(in))
		if err == nil || !strings.Contains(err.Error(), "sqlc issue #4191") {
			t.Errorf("expected the duplicate parameter error, got %v", err)
		}
		if len(res.Diagnostics) != 1 {
			t.Errorf("same fragment twice must not produce the cross-site note: %v", res.Diagnostics)
		}
		ex.ParamLimit = 1
		if _, err := ex.Expand("q.sql", []byte(in)); err != nil {
			t.Errorf("with the default parameter limit the repeat is fine: %v", err)
		}
	})
	t.Run("sqlite slice must be last", func(t *testing.T) {
		lib := mustLib(t, SQLite, "-- fragment: in_ids(a) :where\n{a}.id IN (sqlc.slice(ids));")
		ex := &Expander{Engine: SQLite, Lib: lib}
		in := "SELECT 1 FROM authors a WHERE /*%where in_ids(a)*/ TRUE /*%end*/ AND a.org_id = sqlc.arg(org_id);"
		res, err := ex.Expand("q.sql", []byte(in))
		if err == nil || !strings.Contains(err.Error(), "sqlc issue #2452") {
			t.Errorf("expected the slice order error, got %v", err)
		}
		if got := res.Diagnostics[0].Pos.String(); got != "frags.sql:2:12" {
			t.Errorf("error position should be the slice inside the fragment, got %s", got)
		}
		in = "SELECT 1 FROM authors a WHERE a.org_id = sqlc.arg(org_id) AND /*%where in_ids(a)*/ TRUE /*%end*/;"
		if res, err := ex.Expand("q.sql", []byte(in)); err != nil || len(res.Diagnostics) != 0 {
			t.Errorf("slice last is fine: %v %v", err, res.Diagnostics)
		}
	})
	t.Run("postgresql slice in the query body warns", func(t *testing.T) {
		ex := &Expander{Engine: PostgreSQL}
		res, err := ex.Expand("q.sql", []byte("SELECT 1 FROM t WHERE id IN (sqlc.slice(ids));"))
		if err != nil || len(res.Diagnostics) != 1 || res.Diagnostics[0].Severity != Warning {
			t.Errorf("expected one warning, got err=%v diags=%v", err, res.Diagnostics)
		}
	})
	t.Run("a name bound by a fragment and the query is noted", func(t *testing.T) {
		lib := mustLib(t, PostgreSQL, testFragments)
		ex := &Expander{Engine: PostgreSQL, Lib: lib}
		in := "SELECT 1 FROM authors a WHERE /*%where visible(a)*/ TRUE /*%end*/ AND a.org_id = @org_id;"
		res, err := ex.Expand("q.sql", []byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Diagnostics) != 1 || res.Diagnostics[0].Severity != Info || !strings.Contains(res.Diagnostics[0].Message, `"org_id" is bound by fragment visible and the query`) {
			t.Errorf("expected the cross-site note, got %v", res.Diagnostics)
		}
		if res.Diagnostics[0].Pos.String() != "frags.sql:5:41" {
			t.Errorf("note position: %s", res.Diagnostics[0].Pos)
		}
	})
}
