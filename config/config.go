// Package config reads sqlcshare settings from sqlc.yaml, where they live in
// the top-level options.sqlcshare block that sqlc leaves unvalidated, or from
// a standalone sqlcshare.yaml with the same shape:
//
//	options:
//	  sqlcshare:
//	    fragments: fragments      # file, directory, glob, or a list of them
//	    sources: queries          # same; these must NOT be listed in sql[].queries
//	    out: gen/sql              # the directory a sql block lists under queries
//	    engine: postgresql        # optional; inferred from the block reading out
//	    marker: false             # insert /* sqlcshare */ after each statement's first keyword
//	    provenance: true          # insert -- sqlcshare: comments
//
// Several expansions are configured under targets:, each with the same keys.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/vichugof/sqlcshare"
	"gopkg.in/yaml.v3"
)

// SidecarName is the standalone configuration file looked for next to a
// sqlc.yaml that has no options.sqlcshare block.
const SidecarName = "sqlcshare.yaml"

// File is a loaded configuration.
type File struct {
	Path   string           // configuration file as given
	Dir    string           // directory relative paths resolve against
	Config sqlcshare.Config // Root is Dir
	Blocks []Block          // sql blocks of sqlc.yaml, when one was read
	Go     []GoPackage      // Go packages that compile a target's output
}

// Block is the part of a sqlc sql block that matters here.
type Block struct {
	Engine     string
	Queries    []string
	ParamLimit int
	Go         *GoGen // the block's gen.go, when it has one
}

// GoGen is the part of a sql block's gen.go that the Go-side generator needs.
type GoGen struct {
	Out         string // relative to Dir
	Package     string
	Rename      map[string]string // column name -> Go name
	Initialisms []string          // nil when unset, which means sqlc's default
}

// GoPackage is a Go package sqlc generates together with the targets whose
// expanded files it compiles.
type GoPackage struct {
	GoGen
	Targets []int // indexes into Config.Targets
}

type stringList []string

func (l *stringList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		var s string
		if err := n.Decode(&s); err != nil {
			return err
		}
		*l = stringList{s}
		return nil
	case yaml.SequenceNode:
		var ss []string
		if err := n.Decode(&ss); err != nil {
			return err
		}
		*l = ss
		return nil
	}
	return fmt.Errorf("line %d: want a string or a list of strings", n.Line)
}

type targetYAML struct {
	Engine     string     `yaml:"engine"`
	Fragments  stringList `yaml:"fragments"`
	Sources    stringList `yaml:"sources"`
	Out        string     `yaml:"out"`
	Marker     bool       `yaml:"marker"`
	Provenance *bool      `yaml:"provenance"`
}

type shareYAML struct {
	Targets    []targetYAML `yaml:"targets"`
	Engine     string       `yaml:"engine"`
	Fragments  stringList   `yaml:"fragments"`
	Sources    stringList   `yaml:"sources"`
	Out        string       `yaml:"out"`
	Marker     bool         `yaml:"marker"`
	Provenance *bool        `yaml:"provenance"`
}

func (s *shareYAML) targets() []targetYAML {
	if len(s.Targets) > 0 {
		return s.Targets
	}
	if s.Sources != nil || s.Out != "" || s.Fragments != nil {
		return []targetYAML{{Engine: s.Engine, Fragments: s.Fragments, Sources: s.Sources, Out: s.Out, Marker: s.Marker, Provenance: s.Provenance}}
	}
	return nil
}

type sqlcYAML struct {
	Version string `yaml:"version"`
	SQL     []struct {
		Engine  string     `yaml:"engine"`
		Queries stringList `yaml:"queries"`
		Gen     struct {
			Go *struct {
				Out                 string            `yaml:"out"`
				Package             string            `yaml:"package"`
				QueryParameterLimit *int              `yaml:"query_parameter_limit"`
				Rename              map[string]string `yaml:"rename"`
				Initialisms         *[]string         `yaml:"initialisms"`
			} `yaml:"go"`
		} `yaml:"gen"`
	} `yaml:"sql"`
	Options struct {
		Sqlcshare *shareYAML `yaml:"sqlcshare"`
	} `yaml:"options"`
}

// Load reads the configuration at path. When path is a sqlc.yaml without an
// options.sqlcshare block, SidecarName next to it is read instead.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sq sqlcYAML
	if err := yaml.Unmarshal(data, &sq); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	f := &File{Path: path, Dir: filepath.Dir(path)}
	for _, b := range sq.SQL {
		blk := Block{Engine: b.Engine, Queries: b.Queries, ParamLimit: 1}
		if g := b.Gen.Go; g != nil {
			if g.QueryParameterLimit != nil {
				blk.ParamLimit = *g.QueryParameterLimit
			}
			if g.Out != "" {
				blk.Go = &GoGen{Out: clean(g.Out), Package: g.Package, Rename: g.Rename}
				if g.Initialisms != nil {
					blk.Go.Initialisms = *g.Initialisms
				}
			}
		}
		f.Blocks = append(f.Blocks, blk)
	}
	share := sq.Options.Sqlcshare
	switch {
	case share != nil:
	case len(sq.SQL) == 0:
		share = new(shareYAML)
		if err := yaml.Unmarshal(data, share); err != nil {
			return nil, fmt.Errorf("%s: %v", path, err)
		}
	default:
		side := filepath.Join(f.Dir, SidecarName)
		sdata, err := os.ReadFile(side)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s: no options.sqlcshare block, and no %s next to it", path, SidecarName)
		}
		if err != nil {
			return nil, err
		}
		share = new(shareYAML)
		if err := yaml.Unmarshal(sdata, share); err != nil {
			return nil, fmt.Errorf("%s: %v", side, err)
		}
	}
	targets := share.targets()
	if len(targets) == 0 {
		return nil, fmt.Errorf("%s: no sqlcshare targets: set sources and out", path)
	}
	f.Config.Root = f.Dir
	for i, t := range targets {
		tgt, err := f.resolveTarget(i, t)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", path, err)
		}
		f.Config.Targets = append(f.Config.Targets, tgt)
	}
	for _, b := range f.Blocks {
		if b.Go == nil {
			continue
		}
		gp := GoPackage{GoGen: *b.Go}
		for i, t := range f.Config.Targets {
			for _, q := range b.Queries {
				if readsDir(q, t.Out) {
					gp.Targets = append(gp.Targets, i)
					break
				}
			}
		}
		if len(gp.Targets) > 0 {
			f.Go = append(f.Go, gp)
		}
	}
	return f, nil
}

func clean(p string) string {
	return path.Clean(strings.ReplaceAll(p, "\\", "/"))
}

// readsDir reports whether a sql block queries entry reads the directory dir.
func readsDir(entry, dir string) bool {
	entry = clean(entry)
	if strings.ContainsAny(entry, "*?[") {
		entry = path.Dir(entry)
	}
	return entry == dir
}

func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func (f *File) resolveTarget(i int, t targetYAML) (sqlcshare.Target, error) {
	var tgt sqlcshare.Target
	if t.Out == "" {
		return tgt, fmt.Errorf("target %d: out is required", i)
	}
	if len(t.Sources) == 0 {
		return tgt, fmt.Errorf("target %d: sources is required", i)
	}
	out := clean(t.Out)
	inputs := append(append([]string(nil), t.Sources...), t.Fragments...)
	for bi, b := range f.Blocks {
		for _, q := range b.Queries {
			for _, p := range inputs {
				if overlaps(clean(q), clean(p)) {
					return tgt, fmt.Errorf("target %d: sql block %d reads queries: %s, which overlaps sqlcshare input %s; sqlc must read only %s (sqlc fmt would strip the directives from the sources)", i, bi, q, p, out)
				}
			}
		}
	}
	var readers []Block
	for _, b := range f.Blocks {
		for _, q := range b.Queries {
			if readsDir(q, out) {
				readers = append(readers, b)
				break
			}
		}
	}
	engineName := t.Engine
	if engineName == "" {
		if len(readers) == 0 {
			return tgt, fmt.Errorf("target %d: no sql block reads queries: %s, so the engine cannot be inferred; set engine or point a sql block at %s", i, out, out)
		}
		engineName = readers[0].Engine
		for _, b := range readers[1:] {
			if b.Engine != engineName {
				return tgt, fmt.Errorf("target %d: sql blocks with engines %s and %s both read %s; set engine explicitly", i, engineName, b.Engine, out)
			}
		}
	}
	engine, err := sqlcshare.ParseEngine(engineName)
	if err != nil {
		return tgt, fmt.Errorf("target %d: %v", i, err)
	}
	limit := 1
	for _, b := range readers {
		if b.ParamLimit > limit {
			limit = b.ParamLimit
		}
	}
	tgt = sqlcshare.Target{
		Engine:       engine,
		Fragments:    []string(t.Fragments),
		Sources:      []string(t.Sources),
		Out:          out,
		Marker:       t.Marker,
		NoProvenance: t.Provenance != nil && !*t.Provenance,
		ParamLimit:   limit,
	}
	return tgt, nil
}
