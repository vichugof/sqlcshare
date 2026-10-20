package sqlcshare

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGenerate(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"fragments/f.sql": "-- fragment: visible(a) :where\n{a}.deleted_at IS NULL;\n",
		"queries/a.sql":   "-- name: A :many\nSELECT * FROM t a WHERE /*%where visible(a)*/ TRUE /*%end*/;\n",
		"queries/b.sql":   "-- name: B :many\nSELECT 1;\n",
	})
	cfg := Config{Root: root, Targets: []Target{{Engine: PostgreSQL, Fragments: []string{"fragments"}, Sources: []string{"queries"}, Out: "gen/sql"}}}

	rep, err := Generate(cfg, Options{})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(rep.Files) != 2 || !rep.Files[0].Changed || !rep.Files[1].Changed {
		t.Fatalf("first run should write both files: %+v", rep.Files)
	}
	got, err := os.ReadFile(filepath.Join(root, "gen", "sql", "a.sql"))
	if err != nil {
		t.Fatal(err)
	}
	want := "-- name: A :many\n-- sqlcshare: queries/a.sql:1 (where visible)\nSELECT * FROM t a WHERE (a.deleted_at IS NULL);\n"
	if string(got) != want {
		t.Errorf("gen/sql/a.sql:\n--- got\n%s\n--- want\n%s", got, want)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "gen", "sql", ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if string(manifest) != "{\n  \"version\": 1,\n  \"files\": [\n    \"a.sql\",\n    \"b.sql\"\n  ]\n}\n" {
		t.Errorf("manifest:\n%s", manifest)
	}
	if m := rep.Maps(); m["gen/sql/a.sql"] == nil || m["gen/sql/a.sql"].Source != "queries/a.sql" {
		t.Errorf("maps: %v", m)
	}

	// Second run: nothing changes.
	rep, err = Generate(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Files {
		if f.Changed {
			t.Errorf("%s changed on an identical rerun", f.Out)
		}
	}
	if _, err := Generate(cfg, Options{Check: true}); err != nil {
		t.Errorf("check mode on up-to-date outputs: %v", err)
	}

	// Edit a source: check mode reports drift without writing.
	writeFiles(t, root, map[string]string{"queries/b.sql": "-- name: B :many\nSELECT 2;\n"})
	rep, err = Generate(cfg, Options{Check: true})
	if !errors.Is(err, ErrDrift) {
		t.Fatalf("check mode: got %v, want ErrDrift", err)
	}
	if !rep.Files[1].Changed || rep.Files[0].Changed {
		t.Errorf("check mode should flag only b.sql: %+v", rep.Files)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "gen", "sql", "b.sql")); strings.Contains(string(got), "SELECT 2") {
		t.Error("check mode must not write")
	}

	// Remove a source: its output is removed because the manifest lists it.
	if err := os.Remove(filepath.Join(root, "queries", "b.sql")); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, map[string]string{"gen/sql/handwritten.sql": "SELECT 'kept';"})
	rep, err = Generate(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != "gen/sql/b.sql" {
		t.Errorf("removed: %v", rep.Removed)
	}
	if _, err := os.Stat(filepath.Join(root, "gen", "sql", "b.sql")); !errors.Is(err, os.ErrNotExist) {
		t.Error("stale output was not removed")
	}
	if _, err := os.Stat(filepath.Join(root, "gen", "sql", "handwritten.sql")); err != nil {
		t.Error("a file not in the manifest must never be removed")
	}

	// Errors: nothing is written.
	writeFiles(t, root, map[string]string{"queries/c.sql": "SELECT /*%columns nope*/;"})
	rep, err = Generate(cfg, Options{})
	if err == nil || !strings.Contains(err.Error(), `unknown fragment "nope"`) {
		t.Fatalf("expected the unknown fragment error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "gen", "sql", "c.sql")); err == nil {
		t.Error("nothing may be written when a target has errors")
	}
	if len(rep.Files) != 0 {
		t.Errorf("report should list no files for a failed target: %+v", rep.Files)
	}
}

func TestCheckConfig(t *testing.T) {
	base := Target{Engine: PostgreSQL, Fragments: []string{"fragments"}, Sources: []string{"queries"}, Out: "gen/sql"}
	tests := []struct {
		name    string
		targets []Target
		want    string
	}{
		{"engine unset", []Target{{Sources: []string{"q"}, Out: "gen"}}, "engine is not set"},
		{"no sources", []Target{{Engine: MySQL, Out: "gen"}}, "no sources"},
		{"out is root", []Target{{Engine: MySQL, Sources: []string{"q"}, Out: "."}}, "out must be a directory inside the root"},
		{"out escapes root", []Target{{Engine: MySQL, Sources: []string{"q"}, Out: "../gen"}}, "out must be a directory inside the root"},
		{"out contains sources", []Target{{Engine: MySQL, Sources: []string{"gen/queries"}, Out: "gen"}}, "overlaps input path"},
		{"out inside fragments dir", []Target{{Engine: MySQL, Sources: []string{"q"}, Fragments: []string{"sql"}, Out: "sql/gen"}}, "overlaps input path"},
		{"duplicate out", []Target{base, base}, "is used by another target"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkConfig(Config{Targets: tt.targets})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}
	if err := checkConfig(Config{Targets: []Target{base}}); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}
