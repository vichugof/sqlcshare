// Package postgresql checks PostgreSQL syntax with sqlc-dev/oliphant, the
// pure Go port of libpg_query that sqlc uses. sqlc's macros parse as function
// calls, so expanded statements are checked as written.
package postgresql

import (
	"errors"

	"github.com/sqlc-dev/oliphant"
	oparser "github.com/sqlc-dev/oliphant/parser"
	"github.com/vichugof/sqlcshare"
)

// Checker implements sqlcshare.Checker for PostgreSQL.
type Checker struct{}

// Check parses sql and reports the first syntax error with its byte offset.
func (Checker) Check(sql string) *sqlcshare.SyntaxError {
	_, err := oliphant.Parse(sql)
	if err == nil {
		return nil
	}
	var pe *oparser.Error
	if errors.As(err, &pe) {
		return &sqlcshare.SyntaxError{Offset: charOffset(sql, pe.Cursorpos), Message: pe.Message}
	}
	return &sqlcshare.SyntaxError{Offset: -1, Message: err.Error()}
}

// charOffset converts libpg_query's 1-based character cursor into a byte
// offset.
func charOffset(sql string, cursor int) int {
	if cursor <= 0 {
		return -1
	}
	n := 1
	for i := range sql {
		if n == cursor {
			return i
		}
		n++
	}
	return len(sql)
}
