package sqlcshare

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// TestGolden expands the corpus under testdata/golden/<engine> and compares
// the result with want/. When sqlc is installed, it also runs sqlc generate
// and sqlc vet over the expanded files, which is the Phase 1 exit criterion.
func TestGolden(t *testing.T) {
	for _, engine := range []Engine{PostgreSQL, MySQL, SQLite} {
		t.Run(engine.String(), func(t *testing.T) {
			dir := filepath.Join("testdata", "golden", engine.String())
			fsys := os.DirFS(dir)
			lib, diags := Load(engine, fsys, "fragments")
			if diags.HasErrors() {
				t.Fatalf("load fragments:\n%v", diags)
			}
			srcs, err := resolveSQLFiles(fsys, []string{"queries"})
			if err != nil {
				t.Fatal(err)
			}
			ex := &Expander{Engine: engine, Lib: lib, Marker: true}
			outputs := map[string][]byte{}
			for _, s := range srcs {
				src, err := os.ReadFile(filepath.Join(dir, s))
				if err != nil {
					t.Fatal(err)
				}
				res, err := ex.Expand(s, src)
				if err != nil {
					t.Fatalf("expand %s:\n%v", s, err)
				}
				for _, d := range res.Diagnostics {
					t.Logf("%s", d)
				}
				base := filepath.Base(s)
				outputs[base] = res.Text
				wantPath := filepath.Join(dir, "want", base)
				if *update {
					if err := os.WriteFile(wantPath, res.Text, 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(wantPath)
				if err != nil {
					t.Fatalf("%v (run go test -update to create it)", err)
				}
				if !bytes.Equal(want, res.Text) {
					t.Errorf("%s: expansion differs from %s:\n--- want\n%s\n--- got\n%s", s, wantPath, want, res.Text)
				}
				if base == "orgs.sql" {
					// No directives: the marker is the only change.
					if got := bytes.ReplaceAll(res.Text, []byte(" "+MarkerComment), nil); !bytes.Equal(got, src) {
						t.Errorf("%s: a file without directives must pass through unchanged apart from the marker", s)
					}
				}
			}
			runSqlc(t, engine, dir, outputs)
		})
	}
}

// runSqlc compiles the expanded files with the sqlc on PATH, then runs a vet
// rule that requires the marker in every query.
func runSqlc(t *testing.T, engine Engine, dir string, outputs map[string][]byte) {
	t.Helper()
	sqlc, err := exec.LookPath("sqlc")
	if err != nil {
		t.Skip("sqlc not installed; skipping compilation of the expanded corpus")
	}
	tmp := t.TempDir()
	for _, sub := range []string{"schema", "queries"} {
		if err := os.MkdirAll(filepath.Join(tmp, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := os.ReadFile(filepath.Join(dir, "schema", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "schema", "schema.sql"), schema, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, text := range outputs {
		if err := os.WriteFile(filepath.Join(tmp, "queries", name), text, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := fmt.Sprintf(`version: "2"
rules:
  - name: sqlcshare-marker
    message: "query did not come from sqlcshare"
    rule: "!query.sql.contains('%s')"
sql:
  - engine: %q
    schema: schema
    queries: queries
    rules:
      - sqlcshare-marker
    gen:
      go:
        package: golden
        out: out
`, MarkerComment, engine.String())
	if err := os.WriteFile(filepath.Join(tmp, "sqlc.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"generate", "vet"} {
		cmd := exec.Command(sqlc, sub)
		cmd.Dir = tmp
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("sqlc %s on the expanded %s corpus failed: %v\n%s", sub, engine, err, out)
		}
	}
	goSrc, err := os.ReadFile(filepath.Join(tmp, "out", "authors.sql.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goSrc), "// sqlcshare: queries/authors.sql:") {
		t.Errorf("provenance comment did not become a Go doc comment in sqlc's output")
	}
	if strings.Contains(string(goSrc), "/*%") {
		t.Errorf("a directive leaked into sqlc's output")
	}
}
