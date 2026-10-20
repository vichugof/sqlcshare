// Package sqlcparse checks syntax by running the installed sqlc's parse
// subcommand. It reports no position and starts a process per statement, but
// it is sqlc's own parser, so it is the final word on what sqlc accepts.
package sqlcparse

import (
	"bytes"
	"io"
	"os/exec"
	"strings"

	"github.com/vichugof/sqlcshare"
)

// Checker implements sqlcshare.Checker with `sqlc parse --dialect <engine>`.
type Checker struct {
	Engine  sqlcshare.Engine
	Command []string // the sqlc command line; nil means "sqlc"
}

// Check feeds sql to sqlc parse and reports its error, without a position.
func (c Checker) Check(sql string) *sqlcshare.SyntaxError {
	parts := c.Command
	if len(parts) == 0 {
		parts = []string{"sqlc"}
	}
	cmd := exec.Command(parts[0], append(append([]string(nil), parts[1:]...), "parse", "--dialect", c.Engine.String())...)
	cmd.Stdin = strings.NewReader(sql)
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return &sqlcshare.SyntaxError{Offset: -1, Message: message(stderr.String(), err)}
	}
	return nil
}

// message picks sqlc's own error line out of its output.
func message(out string, err error) string {
	var last string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "Error: "); ok {
			return strings.TrimPrefix(rest, "parse error: ")
		}
		if line != "" {
			last = line
		}
	}
	if last != "" {
		return last
	}
	return err.Error()
}
