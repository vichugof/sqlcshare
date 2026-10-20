// Package sqlite checks SQLite syntax with sqlc-dev/meyer, the SQLite parser
// the sqlc project maintains. meyer follows SQLite's grammar exactly, which
// has no schema-qualified function calls, so sqlc's macros are rewritten to
// placeholders before parsing, as sqlc's own preprocessor does; positions are
// mapped back to the original text.
package sqlite

import (
	"errors"

	"github.com/sqlc-dev/meyer/parser"
	"github.com/vichugof/sqlcshare"
)

// Checker implements sqlcshare.Checker for SQLite.
type Checker struct{}

// Check parses sql and reports the first syntax error with its byte offset.
func (Checker) Check(sql string) *sqlcshare.SyntaxError {
	rewritten, back := sqlcshare.RewriteMacros(sqlcshare.SQLite, sql)
	_, err := parser.ParseString(rewritten)
	if err == nil {
		return nil
	}
	var me *parser.Error
	if errors.As(err, &me) {
		off := -1
		if me.Offset >= 0 {
			off = back(me.Offset)
		}
		return &sqlcshare.SyntaxError{Offset: off, Message: me.Message}
	}
	return &sqlcshare.SyntaxError{Offset: -1, Message: err.Error()}
}
