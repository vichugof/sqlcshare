package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vichugof/sqlcshare"
)

func write(t *testing.T, dir, name, text string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadOptionsBlock(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "sqlc.yaml", `version: "2"
options:
  sqlcshare:
    fragments: [fragments, shared/*.sql]
    sources: queries
    out: gen/sql
    marker: true
    provenance: false
sql:
  - engine: mysql
    schema: schema
    queries: gen/sql/*.sql
    gen:
      go:
        package: db
        out: db
        query_parameter_limit: 5
        rename:
          page_size: PerPage
        initialisms: [id, url]
`)
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if f.Dir != dir || f.Config.Root != dir || len(f.Config.Targets) != 1 {
		t.Fatalf("file: %+v", f)
	}
	if len(f.Go) != 1 || f.Go[0].Out != "db" || f.Go[0].Package != "db" || f.Go[0].Rename["page_size"] != "PerPage" || strings.Join(f.Go[0].Initialisms, ",") != "id,url" || len(f.Go[0].Targets) != 1 || f.Go[0].Targets[0] != 0 {
		t.Errorf("go packages: %+v", f.Go)
	}
	tgt := f.Config.Targets[0]
	if tgt.Engine != sqlcshare.MySQL || tgt.ParamLimit != 5 || tgt.Out != "gen/sql" || !tgt.Marker || !tgt.NoProvenance {
		t.Errorf("target: %+v", tgt)
	}
	if strings.Join(tgt.Fragments, ",") != "fragments,shared/*.sql" || strings.Join(tgt.Sources, ",") != "queries" {
		t.Errorf("paths: %+v", tgt)
	}
	if len(f.Blocks) != 1 || f.Blocks[0].ParamLimit != 5 {
		t.Errorf("blocks: %+v", f.Blocks)
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name, yaml, want string
	}{
		{"sources read by sqlc", `version: "2"
options:
  sqlcshare: {sources: queries, out: gen/sql}
sql:
  - engine: postgresql
    schema: schema
    queries: queries
`, "overlaps sqlcshare input queries"},
		{"source file read by sqlc", `version: "2"
options:
  sqlcshare: {sources: queries, out: gen/sql}
sql:
  - engine: postgresql
    schema: schema
    queries: [gen/sql, queries/one.sql]
`, "overlaps sqlcshare input queries"},
		{"engine not inferable", `version: "2"
options:
  sqlcshare: {sources: queries, out: gen/sql}
sql:
  - engine: postgresql
    schema: schema
    queries: other
`, "cannot be inferred"},
		{"ambiguous engine", `version: "2"
options:
  sqlcshare: {sources: queries, out: gen/sql}
sql:
  - {engine: postgresql, schema: schema, queries: gen/sql}
  - {engine: mysql, schema: schema, queries: gen/sql}
`, "set engine explicitly"},
		{"no block and no sidecar", `version: "2"
sql:
  - {engine: postgresql, schema: schema, queries: gen/sql}
`, "no options.sqlcshare block"},
		{"missing out", `version: "2"
options:
  sqlcshare: {sources: queries}
sql:
  - {engine: postgresql, schema: schema, queries: gen/sql}
`, "out is required"},
		{"bad engine", `sources: queries
out: gen/sql
engine: oracle
`, "unknown engine"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := write(t, dir, "sqlc.yaml", tt.yaml)
			_, err := Load(p)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadStandaloneAndSidecar(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "sqlcshare.yaml", `targets:
  - engine: postgresql
    fragments: pg/fragments
    sources: pg/queries
    out: pg/gen
  - engine: sqlite
    sources: lite/queries
    out: lite/gen
`)
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Config.Targets) != 2 || f.Config.Targets[0].Engine != sqlcshare.PostgreSQL || f.Config.Targets[1].Engine != sqlcshare.SQLite {
		t.Errorf("targets: %+v", f.Config.Targets)
	}
	if f.Config.Targets[1].ParamLimit != 1 {
		t.Errorf("default parameter limit: %d", f.Config.Targets[1].ParamLimit)
	}
	// A sqlc.yaml without the block picks up the sidecar, and the engine is
	// inferred from the block that reads the output directory.
	write(t, dir, "sqlcshare.yaml", "sources: queries\nout: pg/gen\n")
	sq := write(t, dir, "sqlc.yaml", `version: "2"
sql:
  - {engine: postgresql, schema: schema, queries: pg/gen, gen: {go: {package: db, out: db, query_parameter_limit: 3}}}
`)
	f, err = Load(sq)
	if err != nil {
		t.Fatal(err)
	}
	if tgt := f.Config.Targets[0]; tgt.Engine != sqlcshare.PostgreSQL || tgt.ParamLimit != 3 || tgt.Out != "pg/gen" {
		t.Errorf("sidecar target: %+v", tgt)
	}
}
