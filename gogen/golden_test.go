package gogen

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vichugof/sqlcshare"
)

var update = flag.Bool("update", false, "rewrite testdata/golden/*/want/sqlcshare.go")

// TestGolden expands the golden corpus, compiles it with the sqlc on PATH,
// generates sqlcshare.go next to sqlc's output, compares it with
// want/sqlcshare.go and type-checks the package: the Phase 4 exit criterion.
func TestGolden(t *testing.T) {
	sqlc, err := exec.LookPath("sqlc")
	if err != nil {
		t.Skip("sqlc not installed")
	}
	for _, engine := range []sqlcshare.Engine{sqlcshare.PostgreSQL, sqlcshare.MySQL, sqlcshare.SQLite} {
		t.Run(engine.String(), func(t *testing.T) {
			dir := filepath.Join("..", "testdata", "golden", engine.String())
			tmp := t.TempDir()
			for _, sub := range []string{"schema"} {
				if err := os.CopyFS(filepath.Join(tmp, sub), os.DirFS(filepath.Join(dir, sub))); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Join(tmp, "queries"), 0o755); err != nil {
				t.Fatal(err)
			}
			fsys := os.DirFS(dir)
			lib, diags := sqlcshare.Load(engine, fsys, "fragments")
			if diags.HasErrors() {
				t.Fatal(diags)
			}
			ex := &sqlcshare.Expander{Engine: engine, Lib: lib}
			var stmts []sqlcshare.Statement
			for _, name := range []string{"authors.sql", "orgs.sql"} {
				src, err := os.ReadFile(filepath.Join(dir, "queries", name))
				if err != nil {
					t.Fatal(err)
				}
				res, err := ex.Expand("queries/"+name, src)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(tmp, "queries", name), res.Text, 0o644); err != nil {
					t.Fatal(err)
				}
				stmts = append(stmts, res.Statements...)
			}
			cfg := fmt.Sprintf("version: \"2\"\nsql:\n  - engine: %q\n    schema: schema\n    queries: queries\n    gen:\n      go:\n        package: golden\n        out: out\n", engine.String())
			if err := os.WriteFile(filepath.Join(tmp, "sqlc.yaml"), []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(sqlc, "generate")
			cmd.Dir = tmp
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("sqlc generate: %v\n%s", err, out)
			}
			res, err := Generate(Package{Dir: filepath.Join(tmp, "out"), Statements: stmts}, false)
			if err != nil {
				t.Fatalf("gogen: %v", err)
			}
			for _, d := range res.Diagnostics {
				t.Logf("%s", d)
			}
			wantPath := filepath.Join(dir, "want", "sqlcshare.go")
			if *update {
				if err := os.WriteFile(wantPath, res.Text, 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				want, err := os.ReadFile(wantPath)
				if err != nil {
					t.Fatalf("%v (run go test -update to create it)", err)
				}
				if !bytes.Equal(want, res.Text) {
					t.Errorf("sqlcshare.go differs from %s:\n--- want\n%s\n--- got\n%s", wantPath, want, res.Text)
				}
			}
			compile(t, filepath.Join(tmp, "out"))
		})
	}
}
