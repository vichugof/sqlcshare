// Package check provides syntax checkers for the three sqlc engines, built on
// the parsers the sqlc project itself uses: oliphant for PostgreSQL, marino
// for MySQL and meyer for SQLite. All three are pure Go. Importing this
// package links all of them; import an engine package directly to link one.
package check

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/vichugof/sqlcshare"
	"github.com/vichugof/sqlcshare/check/mysql"
	"github.com/vichugof/sqlcshare/check/postgresql"
	"github.com/vichugof/sqlcshare/check/sqlcparse"
	"github.com/vichugof/sqlcshare/check/sqlite"
)

// For returns the built-in checker for engine, or nil for an unknown engine.
func For(engine sqlcshare.Engine) sqlcshare.Checker {
	switch engine {
	case sqlcshare.PostgreSQL:
		return postgresql.Checker{}
	case sqlcshare.MySQL:
		return mysql.Checker{}
	case sqlcshare.SQLite:
		return sqlite.Checker{}
	}
	return nil
}

// Builtin returns the built-in checkers keyed by engine, ready for
// sqlcshare.Options.Checkers.
func Builtin() map[sqlcshare.Engine]sqlcshare.Checker {
	return map[sqlcshare.Engine]sqlcshare.Checker{
		sqlcshare.PostgreSQL: postgresql.Checker{},
		sqlcshare.MySQL:      mysql.Checker{},
		sqlcshare.SQLite:     sqlite.Checker{},
	}
}

// SqlcParse returns checkers that run the parse subcommand of the given sqlc
// command line (for example "sqlc" or "go tool sqlc") for every engine. They
// report no positions and start a process per statement, but they are the
// installed sqlc's own parsers. The command must support `sqlc parse`, which
// arrived in sqlc 1.31.0.
func SqlcParse(command string) (map[sqlcshare.Engine]sqlcshare.Checker, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("sqlcshare: empty sqlc command")
	}
	if _, err := exec.LookPath(parts[0]); err != nil {
		return nil, err
	}
	probe := exec.Command(parts[0], append(parts[1:], "parse", "--help")...)
	if out, err := probe.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("sqlcshare: %q does not support `sqlc parse` (needs sqlc 1.31 or later): %s", command, strings.TrimSpace(string(out)))
	}
	checkers := map[sqlcshare.Engine]sqlcshare.Checker{}
	for _, e := range []sqlcshare.Engine{sqlcshare.PostgreSQL, sqlcshare.MySQL, sqlcshare.SQLite} {
		checkers[e] = sqlcparse.Checker{Engine: e, Command: parts}
	}
	return checkers, nil
}
