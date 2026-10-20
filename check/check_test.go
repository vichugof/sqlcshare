package check

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vichugof/sqlcshare"
	"github.com/vichugof/sqlcshare/check/mysql"
	"github.com/vichugof/sqlcshare/check/postgresql"
	"github.com/vichugof/sqlcshare/check/sqlite"
)

func expectError(t *testing.T, c sqlcshare.Checker, sql string, wantOffset int, wantMsg string) {
	t.Helper()
	se := c.Check(sql)
	if se == nil {
		t.Errorf("%q: expected an error containing %q", sql, wantMsg)
		return
	}
	if se.Offset != wantOffset || !strings.Contains(se.Message, wantMsg) {
		t.Errorf("%q: got offset %d message %q, want offset %d message containing %q", sql, se.Offset, se.Message, wantOffset, wantMsg)
	}
}

func expectOK(t *testing.T, c sqlcshare.Checker, sql string) {
	t.Helper()
	if se := c.Check(sql); se != nil {
		t.Errorf("%q: unexpected error at %d: %s", sql, se.Offset, se.Message)
	}
}

func TestPostgreSQL(t *testing.T) {
	c := postgresql.Checker{}
	expectOK(t, c, "SELECT a.id FROM authors a WHERE a.org_id = sqlc.arg(org_id) AND a.x > sqlc.narg(since)::timestamptz AND a.id = ANY($1::bigint[]) AND b = @name;")
	expectOK(t, c, "-- name: X :many\nWITH r AS (SELECT o.* FROM orders o) SELECT /* sqlcshare */ sqlc.embed(a), count(*) FROM r, authors a WHERE a.id IN (sqlc.slice(ids));\n/* a /* nested */ b */ SELECT $$x;y$$;")
	expectError(t, c, "SELECT a.id, FROM t;", 13, `syntax error at or near "FROM"`)
	expectError(t, c, "SELECT 'ü', FROM t;", 13, `syntax error at or near "FROM"`)
	expectError(t, c, "SELECT 1 FROM t WHERE x = ?;", 27, `syntax error at or near ";"`)
}

func TestMySQL(t *testing.T) {
	c := mysql.Checker{}
	expectOK(t, c, "SELECT a.id FROM authors a WHERE a.org_id = sqlc.arg(org_id) AND a.x > CAST(sqlc.narg(since) AS DATETIME) LIMIT ?;")
	expectOK(t, c, "# hash\n-- dash\nSELECT /* sqlcshare */ sqlc.embed(a), `id` FROM `authors` a WHERE a.id IN (sqlc.slice(ids));")
	expectError(t, c, "SELECT 1 FROM t LIMIT sqlc.arg(n);", 22, `syntax error near "sqlc.arg(n);"`)
	expectError(t, c, "SELECT a.id, FROM t;", 13, `syntax error near "FROM t;"`)
	expectError(t, c, "SELECT 1\nFROM t\nWHERE a = ;", 26, `syntax error near ";"`)
	if se := c.Check("SELECT 1 FROM t WHERE a = ; -- x\n"); se == nil || se.Offset != 26 {
		t.Errorf("trailing text after the error: %+v", se)
	}
}

func TestSQLite(t *testing.T) {
	c := sqlite.Checker{}
	expectOK(t, c, "SELECT sqlc.embed(a) FROM authors a WHERE a.org_id = sqlc.arg(org_id) AND a.id IN (sqlc.slice(ids)) AND b = @name AND c = ?1 LIMIT sqlc.arg(n);")
	expectOK(t, c, "-- name: X :one\nWITH r AS (SELECT 1) SELECT /* sqlcshare */ count(*) FROM r; SELECT `id`, \"q\" FROM t; -- end\n")
	expectError(t, c, "SELECT a.id, FROM t;", 13, `near "FROM": syntax error`)
	expectError(t, c, "SELECT 1 FROM t WHERE a = sqlc.arg(x) AND b = ;", 46, `near ";": syntax error`)
}

// TestGoldenCorpora runs every golden corpus through the built-in checkers:
// the fragments through CheckFragments and the queries through an Expander
// with a Checker. They are known to compile with sqlc, so nothing may be
// reported.
func TestGoldenCorpora(t *testing.T) {
	for _, engine := range []sqlcshare.Engine{sqlcshare.PostgreSQL, sqlcshare.MySQL, sqlcshare.SQLite} {
		t.Run(engine.String(), func(t *testing.T) {
			dir := filepath.Join("..", "testdata", "golden", engine.String())
			lib, diags := sqlcshare.Load(engine, os.DirFS(dir), "fragments")
			if len(diags) != 0 {
				t.Fatal(diags)
			}
			if diags := sqlcshare.CheckFragments(engine, lib, For(engine)); len(diags) != 0 {
				t.Errorf("fragment check:\n%v", diags)
			}
			ex := &sqlcshare.Expander{Engine: engine, Lib: lib, Marker: true, Checker: For(engine)}
			for _, name := range []string{"authors.sql", "orgs.sql"} {
				src, err := os.ReadFile(filepath.Join(dir, "queries", name))
				if err != nil {
					t.Fatal(err)
				}
				res, err := ex.Expand("queries/"+name, src)
				if ds := res.Diagnostics.UpTo(sqlcshare.Warning); err != nil || len(ds) != 0 {
					t.Errorf("%s: err=%v diagnostics=%v", name, err, ds)
				}
			}
		})
	}
	if For(sqlcshare.Engine(99)) != nil || len(Builtin()) != 3 {
		t.Error("For/Builtin")
	}
}

func TestSqlcParse(t *testing.T) {
	if _, err := SqlcParse("definitely-not-installed-xyz"); err == nil {
		t.Error("a missing command must be an error")
	}
	cmd := os.Getenv("SQLCSHARE_TEST_SQLC")
	if cmd == "" {
		if p, err := exec.LookPath("sqlc"); err == nil {
			cmd = p
		}
	}
	if cmd == "" {
		t.Skip("no sqlc available")
	}
	checkers, err := SqlcParse(cmd)
	if err != nil {
		t.Skipf("sqlc parse not supported: %v", err)
	}
	c := checkers[sqlcshare.PostgreSQL]
	expectOK(t, c, "SELECT a.id FROM authors a WHERE a.org_id = sqlc.arg(org_id);")
	expectError(t, c, "SELECT a.id, FROM t;", -1, "syntax error")
	expectError(t, checkers[sqlcshare.MySQL], "SELECT 1 FROM t LIMIT sqlc.arg(n);", -1, "syntax error")
}
