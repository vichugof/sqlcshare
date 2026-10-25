package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/vichugof/sqlcshare"
	"github.com/vichugof/sqlcshare/config"
)

// runStats prints, for every target, how many lines the sources, fragments
// and expanded files hold and how often each fragment is used, directly in a
// statement or nested inside another fragment. Nothing is written.
func runStats(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sqlcshare stats", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("f", "sqlc.yaml", "sqlc configuration file, or a standalone sqlcshare.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cf, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "sqlcshare:", err)
		return 1
	}
	rep, err := sqlcshare.Generate(cf.Config, sqlcshare.Options{Check: true})
	if err != nil && !errors.Is(err, sqlcshare.ErrDrift) {
		return report(rep, err, true, false, io.Discard, stderr)
	}
	if errors.Is(err, sqlcshare.ErrDrift) {
		fmt.Fprintln(stderr, "sqlcshare: warning: the expanded files are out of date; run sqlcshare generate")
	}
	for ti, t := range cf.Config.Targets {
		if ti > 0 {
			fmt.Fprintln(stdout)
		}
		printStats(stdout, cf.Dir, t, ti, rep)
	}
	return 0
}

type fragmentStat struct {
	name, kind     string
	direct, nested int             // uses at depth 0, and inside other fragments
	stmts          map[string]bool // statements that use it at any depth
}

func printStats(w io.Writer, dir string, t sqlcshare.Target, ti int, rep *sqlcshare.Report) {
	stats := map[string]*fragmentStat{}
	stat := func(name, kind string) *fragmentStat {
		s := stats[name]
		if s == nil {
			s = &fragmentStat{name: name, kind: kind, stmts: map[string]bool{}}
			stats[name] = s
		}
		return s
	}
	var fragFiles []string
	fragLines, definitions := 0, 0
	if lib := rep.Libraries[ti]; lib != nil {
		seen := map[string]bool{}
		for _, f := range lib.Fragments() {
			definitions++
			stat(f.Name, f.Kind.String())
			if !seen[f.Pos.File] {
				seen[f.Pos.File] = true
				fragFiles = append(fragFiles, f.Pos.File)
				fragLines += countLines(readRel(dir, f.Pos.File))
			}
		}
	}
	var files, srcLines, outLines, stmts, using, derived int
	for _, f := range rep.Files {
		if f.Target != ti {
			continue
		}
		files++
		srcLines += countLines(readRel(dir, f.Source))
		outLines += countLines(f.Text)
		for i, st := range f.Statements {
			stmts++
			if st.Derived != sqlcshare.DeriveNone {
				derived++
			}
			if len(st.Uses) > 0 {
				using++
			}
			key := fmt.Sprintf("%s#%d", f.Source, i)
			for _, u := range st.Uses {
				s := stat(u.Name, u.Kind.String())
				if u.Depth == 0 {
					s.direct++
				} else {
					s.nested++
				}
				s.stmts[key] = true
			}
		}
	}
	fmt.Fprintf(w, "%s (%v)\n", t.Out, t.Engine)
	fmt.Fprintf(w, "  sources    %4d files %6d lines\n", files, srcLines)
	fmt.Fprintf(w, "  fragments  %4d files %6d lines %4d definitions\n", len(fragFiles), fragLines, definitions)
	fmt.Fprintf(w, "  expanded   %4d files %6d lines\n", files, outLines)
	pct := 0
	if stmts > 0 {
		pct = using * 100 / stmts
	}
	fmt.Fprintf(w, "  statements %4d, %d using a fragment (%d%%), %d derived\n", stmts, using, pct, derived)
	if len(stats) == 0 {
		return
	}
	list := make([]*fragmentStat, 0, len(stats))
	for _, s := range stats {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if len(a.stmts) != len(b.stmts) {
			return len(a.stmts) > len(b.stmts)
		}
		if a.direct+a.nested != b.direct+b.nested {
			return a.direct+a.nested > b.direct+b.nested
		}
		return a.name < b.name
	})
	fmt.Fprintf(w, "  %-36s %-8s %6s %6s %10s\n", "fragment", "kind", "direct", "nested", "statements")
	for _, s := range list {
		fmt.Fprintf(w, "  %-36s %-8s %6d %6d %10d\n", s.name, s.kind, s.direct, s.nested, len(s.stmts))
	}
}

func readRel(dir, rel string) []byte {
	b, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	return b
}

func countLines(b []byte) int {
	n := bytes.Count(b, []byte("\n"))
	if len(b) > 0 && b[len(b)-1] != '\n' {
		n++
	}
	return n
}
