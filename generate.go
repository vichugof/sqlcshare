package sqlcshare

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ManifestName is the file Generate keeps in every output directory. It lists
// the files Generate wrote there, so that the next run can remove outputs
// whose source disappeared without touching anything else. sqlc ignores it
// because it is not a .sql file.
const ManifestName = "sqlcshare.manifest.json"

// Target is one expansion: fragment files, source files, and the output
// directory a sqlc block lists under queries.
type Target struct {
	Engine Engine
	// Fragments and Sources are files, directories or globs, relative to
	// Config.Root. Directories are listed the way sqlc lists a queries
	// directory: .sql files one level deep, hidden and .down.sql files skipped.
	Fragments []string
	Sources   []string
	// Out is the output directory, relative to Config.Root. Every source file
	// is written to Out under its base name, so base names must be unique.
	Out          string
	Marker       bool
	NoProvenance bool
	ParamLimit   int // query_parameter_limit of the consuming sqlc block
}

// Config is the input of Generate.
type Config struct {
	Root    string // directory relative paths resolve against; empty means "."
	Targets []Target
}

// Options controls Generate.
type Options struct {
	// Check writes nothing and makes Generate return ErrDrift when any output
	// would change.
	Check bool
	// Checkers, keyed by engine, parse every fragment and every expanded
	// statement before anything is written. See package check.
	Checkers map[Engine]Checker
}

// GeneratedFile is one expanded source file.
type GeneratedFile struct {
	Target     int    // index into Config.Targets
	Source     string // relative to Config.Root
	Out        string // relative to Config.Root
	Text       []byte
	Map        *SourceMap
	Statements []Statement
	Changed    bool // the file on disk differed (or was missing)
}

// Report is the output of Generate.
type Report struct {
	Files       []GeneratedFile
	Removed     []string   // stale outputs removed (or, with Check, that would be)
	Libraries   []*Library // the fragments of each target, by index; nil when the target failed
	Diagnostics Diagnostics
}

// Maps returns the source maps keyed by output path, as RewriteDiagnostics
// expects when sqlc runs in Config.Root.
func (r *Report) Maps() map[string]*SourceMap {
	m := make(map[string]*SourceMap, len(r.Files))
	for _, f := range r.Files {
		m[f.Out] = f.Map
	}
	return m
}

// ErrDrift is returned by Generate with Options.Check set when an output
// would change.
var ErrDrift = errors.New("sqlcshare: generated files are out of date")

// Generate expands every target. Nothing is written for a target with an
// error-severity diagnostic. The returned error is the Diagnostics when any
// has severity Error, ErrDrift in check mode when outputs would change, or an
// I/O or configuration error.
func Generate(cfg Config, opts Options) (*Report, error) {
	root := cfg.Root
	if root == "" {
		root = "."
	}
	if err := checkConfig(cfg); err != nil {
		return nil, err
	}
	fsys := os.DirFS(root)
	rep := &Report{Libraries: make([]*Library, len(cfg.Targets))}
	drift := false
	for ti, t := range cfg.Targets {
		files, lib, diags := expandTarget(fsys, t, opts.Checkers[t.Engine])
		rep.Diagnostics = append(rep.Diagnostics, diags...)
		if diags.HasErrors() {
			continue
		}
		rep.Libraries[ti] = lib
		for i := range files {
			files[i].Target = ti
		}
		removed, changed, err := writeTarget(root, t.Out, files, opts.Check)
		if err != nil {
			return rep, err
		}
		rep.Files = append(rep.Files, files...)
		rep.Removed = append(rep.Removed, removed...)
		drift = drift || changed
	}
	sortDiagnostics(rep.Diagnostics)
	if rep.Diagnostics.HasErrors() {
		return rep, rep.Diagnostics
	}
	if opts.Check && drift {
		return rep, ErrDrift
	}
	return rep, nil
}

// checkConfig rejects targets whose output directory could clobber inputs.
func checkConfig(cfg Config) error {
	outs := map[string]bool{}
	for i, t := range cfg.Targets {
		if t.Engine == engineUnset {
			return fmt.Errorf("sqlcshare: target %d: engine is not set", i)
		}
		if len(t.Sources) == 0 {
			return fmt.Errorf("sqlcshare: target %d: no sources", i)
		}
		out := cleanRel(t.Out)
		if out == "" || out == "." || out == ".." || strings.HasPrefix(out, "../") || path.IsAbs(out) {
			return fmt.Errorf("sqlcshare: target %d: out must be a directory inside the root, got %q", i, t.Out)
		}
		if outs[out] {
			return fmt.Errorf("sqlcshare: target %d: out %q is used by another target", i, t.Out)
		}
		outs[out] = true
		for _, p := range append(append([]string(nil), t.Sources...), t.Fragments...) {
			p = cleanRel(p)
			if p == out || strings.HasPrefix(p, out+"/") || strings.HasPrefix(out, p+"/") {
				return fmt.Errorf("sqlcshare: target %d: out %q overlaps input path %q", i, t.Out, p)
			}
		}
	}
	return nil
}

func cleanRel(p string) string {
	return path.Clean(strings.ReplaceAll(p, "\\", "/"))
}

// expandTarget loads the fragments and expands the sources of one target.
func expandTarget(fsys fs.FS, t Target, checker Checker) ([]GeneratedFile, *Library, Diagnostics) {
	lib, diags := Load(t.Engine, fsys, t.Fragments...)
	if diags.HasErrors() {
		return nil, nil, diags
	}
	diags = append(diags, CheckFragments(t.Engine, lib, checker)...)
	if diags.HasErrors() {
		return nil, nil, diags
	}
	srcs, err := resolveSQLFiles(fsys, t.Sources)
	if err != nil {
		diags.add(Pos{}, Error, "sources: %v", err)
		return nil, nil, diags
	}
	ex := &Expander{Engine: t.Engine, Lib: lib, Marker: t.Marker, NoProvenance: t.NoProvenance, ParamLimit: t.ParamLimit, Checker: checker}
	out := cleanRel(t.Out)
	var files []GeneratedFile
	seen := map[string]string{}
	names := map[string]Pos{}
	for _, s := range srcs {
		outRel := path.Join(out, path.Base(s))
		if prev, dup := seen[outRel]; dup {
			diags.add(Pos{File: s}, Error, "output %s would also be written from %s; source base names must be unique", outRel, prev)
			continue
		}
		seen[outRel] = s
		src, err := fs.ReadFile(fsys, s)
		if err != nil {
			diags.add(Pos{File: s}, Error, "%v", err)
			continue
		}
		res, _ := ex.Expand(s, src)
		if res == nil {
			diags.add(Pos{File: s}, Error, "expansion produced no result")
			continue
		}
		diags = append(diags, res.Diagnostics...)
		for _, st := range res.Statements {
			if st.Name == "" {
				continue
			}
			if prev, dup := names[st.Name]; dup {
				diags.add(st.Pos, Error, "query %q is also defined at %s; sqlc requires unique names", st.Name, prev)
				continue
			}
			names[st.Name] = st.Pos
		}
		res.Map.Out = outRel
		files = append(files, GeneratedFile{Source: s, Out: outRel, Text: res.Text, Map: res.Map, Statements: res.Statements})
	}
	sortDiagnostics(diags)
	return files, lib, diags
}

type manifest struct {
	Version int      `json:"version"`
	Files   []string `json:"files"`
}

// writeTarget writes the files of one target into out, removes outputs listed
// in the previous manifest that no longer have a source, and writes the new
// manifest. With check set it only reports what would change.
func writeTarget(root, out string, files []GeneratedFile, check bool) (removed []string, changed bool, err error) {
	outDir := filepath.Join(root, filepath.FromSlash(cleanRel(out)))
	old, err := readManifest(outDir)
	if err != nil {
		return nil, false, err
	}
	keep := map[string]bool{}
	for i := range files {
		name := path.Base(files[i].Out)
		keep[name] = true
		abs := filepath.Join(outDir, name)
		cur, err := os.ReadFile(abs)
		if err == nil && bytes.Equal(cur, files[i].Text) {
			continue
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, false, err
		}
		files[i].Changed = true
		changed = true
		if check {
			continue
		}
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return nil, false, err
		}
		if err := os.WriteFile(abs, files[i].Text, 0o644); err != nil {
			return nil, false, err
		}
	}
	var oldFiles []string
	if old != nil {
		oldFiles = old.Files
	}
	for _, name := range oldFiles {
		if keep[name] || strings.Contains(name, "/") || strings.Contains(name, "\\") {
			continue
		}
		abs := filepath.Join(outDir, name)
		if _, err := os.Stat(abs); err != nil {
			continue
		}
		removed = append(removed, path.Join(cleanRel(out), name))
		changed = true
		if !check {
			if err := os.Remove(abs); err != nil {
				return nil, false, err
			}
		}
	}
	names := make([]string, 0, len(keep))
	for name := range keep {
		names = append(names, name)
	}
	sort.Strings(names)
	m := manifest{Version: 1, Files: names}
	if !check && (old == nil || !equalStrings(old.Files, names)) {
		data, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return nil, false, err
		}
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return nil, false, err
		}
		if err := os.WriteFile(filepath.Join(outDir, ManifestName), append(data, '\n'), 0o644); err != nil {
			return nil, false, err
		}
	}
	return removed, changed, nil
}

// readManifest returns the manifest in dir, or nil when there is none.
func readManifest(dir string) (*manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("sqlcshare: %s: %v", filepath.Join(dir, ManifestName), err)
	}
	return &m, nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
