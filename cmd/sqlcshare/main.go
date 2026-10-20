// Command sqlcshare expands SQL fragments into the query files that sqlc reads.
//
//	sqlcshare generate [-f sqlc.yaml] [-check] [-no-sqlc] [-no-go] [-sqlc "sqlc"] [-validate builtin|sqlc|off] [-v]
//	sqlcshare expand -engine ENGINE -sources PATHS -out DIR [-fragments PATHS] [-root DIR] [-marker] [-no-provenance] [-check] [-validate builtin|sqlc|off] [-v]
//	sqlcshare gocode [-f sqlc.yaml] [-check] [-v]
//	sqlcshare version
//
// generate reads the options.sqlcshare block of sqlc.yaml (or sqlcshare.yaml
// next to it), writes the expanded files, runs `sqlc generate` with the same
// configuration file, rewriting sqlc's diagnostics to point at the source and
// fragment files, and then writes sqlcshare.go into every Go package sqlc
// generated (see package gogen). Wire it into go generate with
//
//	//go:generate go tool sqlcshare generate
//
// after `go get -tool github.com/vichugof/sqlcshare/cmd/sqlcshare` and
// `go get -tool github.com/sqlc-dev/sqlc/cmd/sqlc`, passing -sqlc "go tool sqlc".
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/vichugof/sqlcshare"
	"github.com/vichugof/sqlcshare/check"
	"github.com/vichugof/sqlcshare/config"
	"github.com/vichugof/sqlcshare/gogen"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "generate":
		return runGenerate(args[1:], stdout, stderr)
	case "expand":
		return runExpand(args[1:], stdout, stderr)
	case "gocode":
		return runGocode(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, "sqlcshare", version())
		return 0
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "sqlcshare: unknown command %q\n\n", args[0])
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage:
  sqlcshare generate [-f sqlc.yaml] [-check] [-no-sqlc] [-no-go] [-sqlc "sqlc"]
                     [-validate builtin|sqlc|off] [-v]
      expand the sources configured in sqlc.yaml (options.sqlcshare) or in
      sqlcshare.yaml next to it, run "sqlc generate -f <file>", then write
      sqlcshare.go (shared parameter structs) into each Go package
  sqlcshare expand -engine ENGINE -sources PATHS -out DIR [-fragments PATHS]
                   [-root DIR] [-marker] [-no-provenance] [-check]
                   [-validate builtin|sqlc|off] [-v]
      expand without a configuration file; PATHS are comma-separated
  sqlcshare gocode [-f sqlc.yaml] [-check] [-v]
      write only sqlcshare.go, from the Go code sqlc generated earlier
  -validate builtin (default) parses fragments and expanded statements with
      the parsers sqlc uses and reports errors at their source position;
      sqlc runs the installed sqlc's parser instead (no positions, slower)
  sqlcshare version
`)
}

func runGenerate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sqlcshare generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("f", "sqlc.yaml", "sqlc configuration file, or a standalone sqlcshare.yaml")
	check := fs.Bool("check", false, "write nothing; exit 1 when any output would change")
	noSqlc := fs.Bool("no-sqlc", false, "do not run sqlc after expanding (nor write sqlcshare.go)")
	noGo := fs.Bool("no-go", false, "do not write sqlcshare.go into the Go packages sqlc generated")
	sqlcCmd := fs.String("sqlc", "sqlc", `command that runs sqlc, for example "go tool sqlc"`)
	validate := fs.String("validate", "builtin", "syntax validation: builtin, sqlc or off")
	verbose := fs.Bool("v", false, "print informational diagnostics and every output file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	checkers, err := checkersFor(*validate, *sqlcCmd)
	if err != nil {
		fmt.Fprintln(stderr, "sqlcshare:", err)
		return 2
	}
	cf, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "sqlcshare:", err)
		return 1
	}
	rep, err := sqlcshare.Generate(cf.Config, sqlcshare.Options{Check: *check, Checkers: checkers})
	code := report(rep, err, *check, *verbose, stdout, stderr)
	if *check {
		if rep != nil && !rep.Diagnostics.HasErrors() && !*noGo {
			if c, drift := runGoGen(cf, rep, true, *verbose, stdout, stderr); c != 0 {
				if drift && code == 0 {
					fmt.Fprintln(stderr, "sqlcshare: generated files are out of date; run sqlcshare generate")
				}
				code = c
			}
		}
		return code
	}
	if code != 0 || *noSqlc {
		return code
	}
	if code := runSqlc(*sqlcCmd, cf, rep, stdout, stderr); code != 0 || *noGo {
		return code
	}
	code, _ = runGoGen(cf, rep, false, *verbose, stdout, stderr)
	return code
}

// runGocode writes sqlcshare.go from the Go packages sqlc generated earlier,
// expanding the sources in memory to learn which fragments each query uses.
func runGocode(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sqlcshare gocode", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("f", "sqlc.yaml", "sqlc configuration file, or a standalone sqlcshare.yaml")
	check := fs.Bool("check", false, "write nothing; exit 1 when sqlcshare.go would change")
	verbose := fs.Bool("v", false, "print informational diagnostics and every output file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cf, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "sqlcshare:", err)
		return 1
	}
	if len(cf.Go) == 0 {
		fmt.Fprintln(stderr, "sqlcshare: no sql block with gen.go reads a sqlcshare output directory")
		return 1
	}
	rep, err := sqlcshare.Generate(cf.Config, sqlcshare.Options{Check: true})
	if err != nil && !errors.Is(err, sqlcshare.ErrDrift) {
		return report(rep, err, true, *verbose, stdout, stderr)
	}
	if errors.Is(err, sqlcshare.ErrDrift) {
		fmt.Fprintln(stderr, "sqlcshare: warning: the expanded files are out of date; run sqlcshare generate")
	}
	for _, d := range rep.Diagnostics.UpTo(sqlcshare.Warning) {
		fmt.Fprintln(stderr, d)
	}
	code, drift := runGoGen(cf, rep, *check, *verbose, stdout, stderr)
	if drift {
		fmt.Fprintln(stderr, "sqlcshare: sqlcshare.go is out of date; run sqlcshare gocode")
	}
	return code
}

// runGoGen writes (or, with check, compares) sqlcshare.go in every Go
// package that compiles a target's output. drift reports that check mode
// found a difference.
func runGoGen(cf *config.File, rep *sqlcshare.Report, check, verbose bool, stdout, stderr io.Writer) (code int, drift bool) {
	for _, gp := range cf.Go {
		pkg := gogen.Package{Dir: filepath.Join(cf.Dir, filepath.FromSlash(gp.Out)), Rename: gp.Rename, Initialisms: gp.Initialisms}
		for _, f := range rep.Files {
			if slices.Contains(gp.Targets, f.Target) {
				pkg.Statements = append(pkg.Statements, f.Statements...)
			}
		}
		rel := path.Join(gp.Out, gogen.FileName)
		res, err := gogen.Generate(pkg, check)
		if res != nil {
			diags := res.Diagnostics
			if !verbose {
				diags = diags.UpTo(sqlcshare.Warning)
			}
			for _, d := range diags {
				fmt.Fprintln(stderr, d)
			}
			switch {
			case check && res.Changed && res.Text == nil:
				fmt.Fprintf(stdout, "stale: %s\n", rel)
				code, drift = 1, true
			case check && res.Changed:
				fmt.Fprintf(stdout, "out of date: %s\n", rel)
				code, drift = 1, true
			case verbose && res.Changed && res.Text == nil:
				fmt.Fprintf(stdout, "removed %s\n", rel)
			case verbose && res.Changed:
				fmt.Fprintf(stdout, "wrote %s\n", rel)
			case verbose && res.Text != nil:
				fmt.Fprintf(stdout, "unchanged %s\n", rel)
			}
		}
		if err != nil {
			var diags sqlcshare.Diagnostics
			if !errors.As(err, &diags) { // diagnostics were printed above
				fmt.Fprintln(stderr, "sqlcshare:", err)
			}
			code = 1
		}
	}
	return code, drift
}

func runExpand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sqlcshare expand", flag.ContinueOnError)
	fs.SetOutput(stderr)
	engineName := fs.String("engine", "", "postgresql, mysql or sqlite")
	fragments := fs.String("fragments", "", "fragment files, directories or globs, comma-separated")
	sources := fs.String("sources", "", "query source files, directories or globs, comma-separated")
	out := fs.String("out", "", "output directory that sqlc reads")
	root := fs.String("root", ".", "directory relative paths resolve against")
	marker := fs.Bool("marker", false, "insert "+sqlcshare.MarkerComment+" after the first keyword of every statement")
	noProv := fs.Bool("no-provenance", false, "do not insert -- sqlcshare: provenance comments")
	check := fs.Bool("check", false, "write nothing; exit 1 when any output would change")
	validate := fs.String("validate", "builtin", "syntax validation: builtin, sqlc or off")
	verbose := fs.Bool("v", false, "print informational diagnostics and every output file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	engine, err := sqlcshare.ParseEngine(*engineName)
	if err != nil {
		fmt.Fprintln(stderr, "sqlcshare:", err)
		return 2
	}
	checkers, err := checkersFor(*validate, "sqlc")
	if err != nil {
		fmt.Fprintln(stderr, "sqlcshare:", err)
		return 2
	}
	cfg := sqlcshare.Config{Root: *root, Targets: []sqlcshare.Target{{
		Engine:       engine,
		Fragments:    splitList(*fragments),
		Sources:      splitList(*sources),
		Out:          *out,
		Marker:       *marker,
		NoProvenance: *noProv,
	}}}
	rep, err := sqlcshare.Generate(cfg, sqlcshare.Options{Check: *check, Checkers: checkers})
	return report(rep, err, *check, *verbose, stdout, stderr)
}

// checkersFor selects the syntax checkers named by the -validate flag.
func checkersFor(mode, sqlcCmd string) (map[sqlcshare.Engine]sqlcshare.Checker, error) {
	switch mode {
	case "builtin":
		return check.Builtin(), nil
	case "sqlc":
		return check.SqlcParse(sqlcCmd)
	case "off", "none", "false":
		return nil, nil
	}
	return nil, fmt.Errorf("-validate must be builtin, sqlc or off, got %q", mode)
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// report prints diagnostics and, when asked, the files written. It returns
// the exit code for the expansion step.
func report(rep *sqlcshare.Report, err error, check, verbose bool, stdout, stderr io.Writer) int {
	if rep != nil {
		diags := rep.Diagnostics
		if !verbose {
			diags = diags.UpTo(sqlcshare.Warning)
		}
		for _, d := range diags {
			fmt.Fprintln(stderr, d)
		}
		for _, f := range rep.Files {
			switch {
			case check && f.Changed:
				fmt.Fprintf(stdout, "out of date: %s\n", f.Out)
			case verbose && f.Changed:
				fmt.Fprintf(stdout, "wrote %s\n", f.Out)
			case verbose:
				fmt.Fprintf(stdout, "unchanged %s\n", f.Out)
			}
		}
		for _, r := range rep.Removed {
			if check {
				fmt.Fprintf(stdout, "stale: %s\n", r)
			} else if verbose {
				fmt.Fprintf(stdout, "removed %s\n", r)
			}
		}
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, sqlcshare.ErrDrift):
		fmt.Fprintln(stderr, "sqlcshare: generated files are out of date; run sqlcshare generate")
		return 1
	}
	var diags sqlcshare.Diagnostics
	if !errors.As(err, &diags) { // diagnostics were printed above
		fmt.Fprintln(stderr, "sqlcshare:", err)
	}
	return 1
}

// runSqlc runs `sqlc generate -f <config>` in the configuration directory and
// rewrites its diagnostics through the source maps.
func runSqlc(cmdline string, cf *config.File, rep *sqlcshare.Report, stdout, stderr io.Writer) int {
	parts := strings.Fields(cmdline)
	if len(parts) == 0 {
		fmt.Fprintln(stderr, "sqlcshare: -sqlc is empty")
		return 2
	}
	args := append(parts[1:], "generate")
	if len(cf.Blocks) > 0 { // otherwise the file is a standalone sqlcshare.yaml and sqlc finds its own
		args = append(args, "-f", filepath.Base(cf.Path))
	}
	cmd := exec.Command(parts[0], args...)
	cmd.Dir = cf.Dir
	cmd.Stdout = stdout
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	err := cmd.Run()
	if rerr := sqlcshare.RewriteDiagnostics(&errOut, stderr, rep.Maps()); rerr != nil {
		fmt.Fprintln(stderr, "sqlcshare:", rerr)
	}
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	fmt.Fprintf(stderr, "sqlcshare: running %s: %v\n", cmdline, err)
	return 1
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "devel"
}
