package sqlcshare

import (
	"strings"
	"testing"
)

const checkFragments = `-- fragment: cols(a) :columns
{a}.id, {a}.name;

-- fragment: bad(a) :where
{a}.x = BAD AND {a}.y = 1;

-- fragment: uses_bad(a) :where
/*%where bad({a})*/ TRUE /*%end*/ AND 1 = 1;

-- fragment: raw :sql
BAD;

-- fragment: cte_ok :cte
SELECT 1 AS n;

-- fragment: j(a, o) :joins
JOIN orgs {o} ON {o}.id = {a}.org_id;
`

// rejecting returns a checker that fails at the first occurrence of marker.
func rejecting(marker string) CheckerFunc {
	return func(sql string) *SyntaxError {
		if i := strings.Index(sql, marker); i >= 0 {
			return &SyntaxError{Offset: i, Message: "near " + marker}
		}
		return nil
	}
}

func TestCheckFragments(t *testing.T) {
	lib := mustLib(t, PostgreSQL, checkFragments)
	var seen []string
	recording := CheckerFunc(func(sql string) *SyntaxError {
		seen = append(seen, sql)
		return nil
	})
	if diags := CheckFragments(PostgreSQL, lib, recording); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	for _, want := range []string{
		"SELECT a.id, a.name FROM sqlcshare_t;",
		"SELECT 1 FROM sqlcshare_t WHERE (a.x = BAD AND a.y = 1);",
		"SELECT 1 FROM sqlcshare_t WHERE ((a.x = BAD AND a.y = 1) AND 1 = 1);",
		"WITH cte_ok AS (SELECT 1 AS n) SELECT 1 FROM cte_ok;",
		"SELECT 1 FROM sqlcshare_t JOIN orgs o ON o.id = a.org_id;",
	} {
		if !contains(seen, want) {
			t.Errorf("template %q was not checked; checked:\n%s", want, strings.Join(seen, "\n"))
		}
	}
	if len(seen) != 5 {
		t.Errorf("checked %d statements, want 5 (the sql fragment has no template)", len(seen))
	}

	diags := CheckFragments(PostgreSQL, lib, rejecting("BAD"))
	if len(diags) != 1 {
		t.Fatalf("want one diagnostic (the error in bad is reported once, not again through uses_bad), got:\n%v", diags)
	}
	if got := diags[0].String(); got != `frags.sql:5:9: error: fragment "bad": syntax error: near BAD` {
		t.Errorf("diagnostic: %s", got)
	}

	diags = CheckFragments(PostgreSQL, lib, rejecting("sqlcshare_t"))
	if len(diags) != 4 || !strings.Contains(diags[0].Message, `fragment "cols" does not parse as a columns fragment: near sqlcshare_t`) || diags[0].Pos.String() != "frags.sql:1:1" {
		t.Errorf("template failures should be reported at the fragment header:\n%v", diags)
	}
	if CheckFragments(PostgreSQL, nil, rejecting("x")) != nil || CheckFragments(PostgreSQL, lib, nil) != nil {
		t.Error("nil library or checker must check nothing")
	}
}

func TestExpandChecker(t *testing.T) {
	lib := mustLib(t, PostgreSQL, checkFragments)
	ex := &Expander{Engine: PostgreSQL, Lib: lib, Checker: rejecting("BAD")}
	res, err := ex.Expand("q.sql", []byte("-- name: X :one\nSELECT BAD FROM t;"))
	if err == nil || len(res.Diagnostics) != 1 || res.Diagnostics[0].String() != "q.sql:2:8: error: syntax error: near BAD" {
		t.Errorf("error in the source: %v", res.Diagnostics)
	}
	res, err = ex.Expand("q.sql", []byte("SELECT 1 FROM t WHERE /*%where bad(t)*/ TRUE /*%end*/;"))
	if err == nil || len(res.Diagnostics) != 1 || res.Diagnostics[0].String() != "frags.sql:5:9: error: syntax error: near BAD (in fragment bad)" {
		t.Errorf("error inside a fragment: %v", res.Diagnostics)
	}
	ex.Checker = CheckerFunc(func(string) *SyntaxError { return &SyntaxError{Offset: -1, Message: "nope"} })
	res, _ = ex.Expand("q.sql", []byte("\n  SELECT 1;"))
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].String() != "q.sql:2:3: error: syntax error: nope" {
		t.Errorf("error without a position goes to the statement: %v", res.Diagnostics)
	}
	res, _ = ex.Expand("q.sql", []byte(";\n"))
	if len(res.Diagnostics) != 0 {
		t.Errorf("an empty statement is not checked: %v", res.Diagnostics)
	}
}

func TestSlotWarnings(t *testing.T) {
	lib := mustLib(t, PostgreSQL, testFragments)
	tests := []struct {
		in   string
		want string // substring of the warning, or empty
	}{
		{"SELECT /*%where visible(a)*/ TRUE /*%end*/ FROM t a;", `is a where fragment but follows "SELECT"`},
		{"SELECT a.id, /*%where visible(a)*/ TRUE /*%end*/ FROM t a;", `follows ","`},
		{"SELECT 1 FROM t a WHERE /*%columns cols(a)*/;", `is a columns fragment but follows "WHERE"`},
		{"SELECT 1 FROM t a WHERE 1 = 1 AND /*%joins org_join(a, o)*/;", `is a joins fragment but follows "AND"`},
		{"SELECT /*%cte recent*/ FROM t;", `is a cte fragment but follows "SELECT"`},
		{"SELECT 1 FROM t a WHERE /*%where visible(a)*/ TRUE /*%end*/;", ""},
		{"SELECT /*%columns cols(a)*/ FROM t a;", ""},
		{"SELECT a.id, /*%columns cols(a)*/ FROM t a;", ""},
		{"SELECT 1 FROM t a /*%joins org_join(a, o)*/;", ""},
		{"WITH /*%cte recent*/ SELECT 1 FROM recent;", ""},
		{"WITH RECURSIVE x AS (SELECT 1), /*%cte recent*/ SELECT 1 FROM recent, x;", ""},
		{"SELECT 1 FROM t a WHERE NOT (/*%where visible(a)*/ TRUE /*%end*/);", ""},
		{"SELECT 1 FROM t a WHERE a.id > 0 /* c */ AND /*%where visible(a)*/ TRUE /*%end*/;", ""},
	}
	for _, tt := range tests {
		res, err := (&Expander{Engine: PostgreSQL, Lib: lib}).Expand("q.sql", []byte(tt.in))
		if err != nil {
			t.Errorf("%q: %v", tt.in, err)
			continue
		}
		var warnings []string
		for _, d := range res.Diagnostics {
			if d.Severity == Warning {
				warnings = append(warnings, d.Message)
			}
		}
		switch {
		case tt.want == "" && len(warnings) > 0:
			t.Errorf("%q: unexpected warnings %v", tt.in, warnings)
		case tt.want != "" && (len(warnings) != 1 || !strings.Contains(warnings[0], tt.want)):
			t.Errorf("%q: want a warning containing %q, got %v", tt.in, tt.want, warnings)
		}
	}
}

func TestRewriteMacros(t *testing.T) {
	in := "SELECT sqlc.embed(a), 'sqlc.arg(x)' FROM a WHERE a.id = sqlc.arg(id) AND a.n IN (sqlc.slice(ids)) AND b = sqlc.narg('n');"
	out, back := RewriteMacros(SQLite, in)
	want := "SELECT a.*, 'sqlc.arg(x)' FROM a WHERE a.id = ? AND a.n IN (?) AND b = ?;"
	if out != want {
		t.Errorf("sqlite:\n got %s\nwant %s", out, want)
	}
	for _, tt := range []struct{ outSub, inSub string }{
		{"SELECT", "SELECT"},
		{"?", "sqlc.arg(id)"},
		{" AND a.n IN", " AND a.n IN"},
		{"(?)", "(sqlc.slice(ids))"},
		{";", ";"},
		{"a.*", "sqlc.embed(a)"},
	} {
		got, wantOff := back(strings.Index(out, tt.outSub)), strings.Index(in, tt.inSub)
		if got != wantOff {
			t.Errorf("offset of %q maps to %d, want %d (%q)", tt.outSub, got, wantOff, tt.inSub)
		}
	}
	if got := back(len(out) + 5); got != len(in) {
		t.Errorf("offset past the end maps to %d, want %d", got, len(in))
	}
	if out, _ := RewriteMacros(PostgreSQL, in); out != "SELECT a.*, 'sqlc.arg(x)' FROM a WHERE a.id = $1 AND a.n IN ($2) AND b = $3;" {
		t.Errorf("postgresql: %s", out)
	}
	if out, _ := RewriteMacros(MySQL, "SELECT 1;"); out != "SELECT 1;" {
		t.Errorf("no macros: %s", out)
	}
}

func TestQuotedMacroNames(t *testing.T) {
	refs := findParams(MySQL, "a = sqlc.arg('x') AND b = sqlc.narg( 'x' ) AND c = sqlc.arg(x) AND d = 'sqlc.arg(y)'")
	if len(refs) != 3 {
		t.Fatalf("want 3 references to x, got %+v", refs)
	}
	for _, r := range refs {
		if r.name != "x" {
			t.Errorf("reference %+v", r)
		}
	}
	_, diags := ParseFragments(PostgreSQL, "f.sql", []byte("-- fragment: f :where\nid IN (sqlc.slice('ids'));"))
	if !diags.HasErrors() {
		t.Error("the quoted form of sqlc.slice must be rejected on PostgreSQL too")
	}
}

func TestPlaceholderInLiteral(t *testing.T) {
	_, diags := ParseFragments(PostgreSQL, "f.sql", []byte("-- fragment: f(a) :where\n{a}.note = '{a}' -- {a} and {x}\n;"))
	if diags.HasErrors() {
		t.Fatal(diags)
	}
	if len(diags) != 2 || diags[0].Pos.String() != "f.sql:2:13" || diags[1].Pos.String() != "f.sql:2:21" {
		t.Errorf("want two warnings at 2:13 and 2:21, got %v", diags)
	}
	for _, d := range diags {
		if d.Severity != Warning || !strings.Contains(d.Message, "{a} inside a literal or comment is not substituted") {
			t.Errorf("unexpected diagnostic %v", d)
		}
	}
}
