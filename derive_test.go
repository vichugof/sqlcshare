package sqlcshare

import (
	"strings"
	"testing"
)

func TestDeriveCount(t *testing.T) {
	lib := mustLib(t, PostgreSQL, testFragments)
	ex := &Expander{Engine: PostgreSQL, Lib: lib, Marker: true}
	tests := []struct {
		name string
		in   string
		want string // the derived statement, from its name line to its semicolon
		info string // expected info message, or empty
		uses string
	}{
		{
			name: "select list replaced, tail dropped, line comment kept on its own line",
			in: "-- name: List :many\n-- derive: CountList :count\n" +
				"SELECT /*%columns cols(a)*/ a.* /*%end*/, 1 AS x\nFROM authors a\n" +
				"WHERE /*%where visible(a)*/ TRUE /*%end*/ -- note\nORDER BY a.id\nLIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);\n",
			want: "-- name: CountList :one\n-- sqlcshare: q.sql:2 (count of List, where visible)\n" +
				"SELECT /* sqlcshare */ count(*) FROM authors a\nWHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id)) -- note\n;",
			uses: "where visible",
		},
		{
			name: "derive comment before the name comment, WITH prefix kept, GROUP BY wrapped",
			in: "-- derive: CountG :count\n-- name: Grouped :many\n" +
				"WITH /*%cte recent*/ recent AS (SELECT 1) /*%end*/\nSELECT a.org_id, count(*) FROM authors a JOIN recent r ON r.id = a.id GROUP BY a.org_id ORDER BY 2 DESC;",
			want: "-- name: CountG :one\n-- sqlcshare: q.sql:1 (count of Grouped, cte recent)\n" +
				"WITH /* sqlcshare */ recent AS (SELECT o.* FROM orders o\nWHERE o.created_at > sqlc.narg(since)::timestamptz)\n" +
				"SELECT count(*) FROM (SELECT a.org_id, count(*) FROM authors a JOIN recent r ON r.id = a.id GROUP BY a.org_id) AS sqlcshare_count;",
			info: "counted through a derived table because the statement uses GROUP",
			uses: "cte recent",
		},
		{
			name: "DISTINCT wrapped, FOR UPDATE dropped, no WITH",
			in:   "-- name: D :many\n-- derive: CountD :count\nSELECT DISTINCT a.name FROM authors a WHERE a.deleted_at IS NULL FOR UPDATE;",
			want: "-- name: CountD :one\n-- sqlcshare: q.sql:2 (count of D)\nSELECT /* sqlcshare */ count(*) FROM (SELECT DISTINCT a.name FROM authors a WHERE a.deleted_at IS NULL) AS sqlcshare_count;",
			info: "DISTINCT",
		},
		{
			name: "ORDER BY inside a sql fragment drops the whole use",
			in:   "-- name: F :many\n-- derive: CountF :count\nSELECT a.id FROM authors a /*%joins org_join(a, o)*/ /*%sql by_id*/ LIMIT 1 /*%end*/;",
			want: "-- name: CountF :one\n-- sqlcshare: q.sql:2 (count of F, joins org_join)\nSELECT /* sqlcshare */ count(*) FROM authors a JOIN orgs o ON o.id = a.org_id;",
			uses: "joins org_join",
		},
		{
			name: "OFFSET as a column name is not a clause",
			in:   "-- name: O :many\n-- derive: CountO :count\nSELECT offset FROM t WHERE offset > 1 OFFSET 2;",
			want: "-- name: CountO :one\n-- sqlcshare: q.sql:2 (count of O)\nSELECT /* sqlcshare */ count(*) FROM t WHERE offset > 1;",
		},
		{
			name: "subqueries and function arguments are not clauses",
			in:   "-- name: S :many\n-- derive: CountS :count\nSELECT (SELECT max(id) FROM b ORDER BY 1 LIMIT 1) AS m, substring(x FROM 1 FOR 2) FROM t WHERE id IN (SELECT id FROM u ORDER BY id) LIMIT 5;",
			want: "-- name: CountS :one\n-- sqlcshare: q.sql:2 (count of S)\nSELECT /* sqlcshare */ count(*) FROM t WHERE id IN (SELECT id FROM u ORDER BY id);",
		},
		{
			name: "UNION wrapped with its ORDER BY dropped",
			in:   "-- name: U :many\n-- derive: CountU :count\nSELECT id FROM a UNION SELECT id FROM b ORDER BY id;",
			want: "-- name: CountU :one\n-- sqlcshare: q.sql:2 (count of U)\nSELECT /* sqlcshare */ count(*) FROM (SELECT id FROM a UNION SELECT id FROM b) AS sqlcshare_count;",
			info: "UNION",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := ex.Expand("q.sql", []byte(tt.in))
			if err != nil {
				t.Fatalf("expand: %v", err)
			}
			got := string(res.Text)
			if !strings.Contains(got, tt.want) {
				t.Errorf("derived statement missing:\n--- want\n%s\n--- got\n%s", tt.want, got)
			}
			if strings.Contains(got, "derive:") {
				t.Errorf("the derive comment must not reach the output:\n%s", got)
			}
			if len(res.Statements) != 2 {
				t.Fatalf("want 2 statements, got %+v", res.Statements)
			}
			d := res.Statements[1]
			if d.Derived != DeriveCount || d.DerivedFrom != res.Statements[0].Name || !strings.HasPrefix(d.Name, "Count") {
				t.Errorf("derived statement record: %+v", d)
			}
			var uses []string
			for _, u := range d.DirectUses() {
				uses = append(uses, u.Kind.String()+" "+u.Name)
			}
			if got := strings.Join(uses, ", "); got != tt.uses {
				t.Errorf("uses: got %q, want %q", got, tt.uses)
			}
			var infos []string
			for _, dg := range res.Diagnostics {
				if dg.Severity == Info {
					infos = append(infos, dg.Message)
				} else {
					t.Errorf("unexpected diagnostic: %v", dg)
				}
			}
			if tt.info == "" && len(infos) > 0 || tt.info != "" && !strings.Contains(strings.Join(infos, "\n"), tt.info) {
				t.Errorf("info diagnostics: got %q, want one containing %q", infos, tt.info)
			}
		})
	}
}

func TestDeriveSourceMap(t *testing.T) {
	lib := mustLib(t, PostgreSQL, testFragments)
	ex := &Expander{Engine: PostgreSQL, Lib: lib}
	in := "-- name: List :many\n-- derive: CountList :count\nSELECT a.id, a.name\nFROM authors a\nWHERE /*%where visible(a)*/ TRUE /*%end*/\nORDER BY a.id;\n"
	res, err := ex.Expand("q.sql", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := "-- name: List :many\n-- sqlcshare: q.sql:1 (where visible)\nSELECT a.id, a.name\nFROM authors a\nWHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id))\nORDER BY a.id;\n\n" +
		"-- name: CountList :one\n-- sqlcshare: q.sql:2 (count of List, where visible)\nSELECT count(*) FROM authors a\nWHERE (a.deleted_at IS NULL AND a.org_id = sqlc.arg(org_id));\n"
	if got := string(res.Text); got != want {
		t.Fatalf("output:\n--- got\n%s\n--- want\n%s", got, want)
	}
	// Line 10 of the output is `SELECT count(*) FROM authors a` of the
	// derived statement: SELECT is the source's SELECT, count(*) is
	// generated, and FROM maps to the source's FROM on line 4.
	for _, tc := range []struct {
		line, col int
		want      Pos
		kind      SegmentKind
		derived   string
	}{
		{10, 1, Pos{"q.sql", 3, 1}, SegSource, "CountList"},
		{10, 7, Pos{"q.sql", 3, 1}, SegGenerated, "CountList"},
		{10, 17, Pos{"q.sql", 4, 1}, SegSource, "CountList"},
		{10, 25, Pos{"q.sql", 4, 9}, SegSource, "CountList"},
		{11, 8, Pos{"frags.sql", 5, 1}, SegFragment, "CountList"},
		{8, 1, Pos{"q.sql", 2, 1}, SegGenerated, "CountList"},
		{9, 1, Pos{"q.sql", 2, 1}, SegGenerated, "CountList"},
		{3, 1, Pos{"q.sql", 3, 1}, SegSource, ""},
	} {
		got, seg, ok := res.Map.Origin(tc.line, tc.col)
		if !ok || got != tc.want || seg.Kind != tc.kind || seg.Derived != tc.derived {
			t.Errorf("origin of %d:%d = %v kind %v derived %q, want %v kind %v derived %q", tc.line, tc.col, got, seg.Kind, seg.Derived, tc.want, tc.kind, tc.derived)
		}
	}
	// A sqlc diagnostic inside the derived statement names it.
	var out strings.Builder
	maps := map[string]*SourceMap{"gen/q.sql": res.Map}
	if err := RewriteDiagnostics(strings.NewReader("gen/q.sql:11:11: column \"deleted_at\" does not exist\n"), &out, maps); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "frags.sql:5:6: column") || !strings.Contains(got, "in derived query CountList") || !strings.Contains(got, "in fragment visible") {
		t.Errorf("rewritten diagnostic: %q", got)
	}
}

func TestDeriveErrors(t *testing.T) {
	lib := mustLib(t, PostgreSQL, testFragments)
	ex := &Expander{Engine: PostgreSQL, Lib: lib}
	tests := []struct {
		in, want string
	}{
		{"-- name: B :many\n-- derive: CountB :count\nINSERT INTO t VALUES (1);", "q.sql:2:1: error: derive CountB: the statement starts with INSERT, not SELECT"},
		{"-- name: B :many\n-- derive: CountB :count\nWITH x AS (SELECT 1) DELETE FROM t;", "derive CountB: the statement is DELETE, not SELECT"},
		{"-- name: N :many\n-- derive: CountN :count\nSELECT 1;", "derive CountN: the SELECT has no FROM clause"},
		{"-- name: Dup :many\n-- derive: Dup :count\nSELECT 1 FROM t;", `derive Dup: a statement named "Dup" already exists at q.sql:3:1`},
		{"-- name: K :many\n-- derive: X :exists\nSELECT 1 FROM t;", "derive X: unknown derivation :exists; only :count is supported"},
		{"-- name: K :many\n-- derive: bad-name :count\nSELECT 1 FROM t;", `derive bad-name: "bad-name" is not a valid query name`},
		{"-- derive: NoName :count\nSELECT 1 FROM t;", "derive NoName: the statement has no -- name: comment"},
		{"-- name: NS :many\n-- derive: CountNS :count\nSELECT 1 FROM t", "derive CountNS: the statement must end with a semicolon"},
		{"-- name: M :many\n-- derive: CountM :count\nSELECT 1 FROM t /*%sql mixed*/;", `derive CountM: fragment "mixed" holds ORDER together with other clauses, so the clause cannot be dropped`},
		{"-- name: W :many\n-- derive: CountW :count\n/*%sql whole*/;", `derive CountW: the SELECT keyword comes from fragment "whole", so the statement cannot be rewritten`},
		{"-- name: Two :many\n-- derive: C :count\n-- derive: C :count\nSELECT 1 FROM t;", `derive C: a statement named "C" already exists at q.sql:2:1`},
	}
	extra := "-- fragment: mixed :sql\nWHERE x = 1 ORDER BY x;\n\n-- fragment: whole :sql\nSELECT 1 FROM t;\n"
	for _, f := range mustLib(t, PostgreSQL, extra).Fragments() {
		lib.Add(f)
	}
	for _, tt := range tests {
		res, err := ex.Expand("q.sql", []byte(tt.in))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q:\n  got  %v\n  want %q", tt.in, err, tt.want)
		}
		if res != nil && strings.Contains(string(res.Text), "-- name: Count") {
			t.Errorf("%q: a derived statement was written despite the error", tt.in)
		}
	}
}

func TestUseParams(t *testing.T) {
	lib := mustLib(t, PostgreSQL, testFragments)
	ex := &Expander{Engine: PostgreSQL, Lib: lib}
	in := "-- name: X :many\nSELECT a.id FROM authors a WHERE /*%where visible_named(a)*/ TRUE /*%end*/ AND a.id = sqlc.arg(id);"
	res, err := ex.Expand("q.sql", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, u := range res.Statements[0].Uses {
		got = append(got, u.Name+"@"+u.Pos.String()+" depth "+string(rune('0'+u.Depth))+" ["+strings.Join(u.Params, ",")+"] def "+u.Def.String())
	}
	want := []string{
		"visible@frags.sql:18:1 depth 1 [org_id] def frags.sql:4:1",
		"visible_named@q.sql:2:34 depth 0 [org_id,name] def frags.sql:17:1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("uses:\n--- got\n%s\n--- want\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// A positional placeholder inside a fragment is flagged.
	mlib := mustLib(t, MySQL, "-- fragment: page :sql\nLIMIT ?;\n")
	mres, err := (&Expander{Engine: MySQL, Lib: mlib}).Expand("q.sql", []byte("-- name: P :many\nSELECT 1 FROM t /*%sql page*/;"))
	if err != nil {
		t.Fatal(err)
	}
	if u := mres.Statements[0].Uses[0]; !u.Positional || len(u.Params) != 0 {
		t.Errorf("positional use: %+v", u)
	}
}
