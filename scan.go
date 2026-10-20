package sqlcshare

import (
	"bytes"
	"sort"
)

// The scanner splits SQL text into regions without parsing it. It only needs
// to know where comments, string literals, quoted identifiers and dollar-quoted
// strings begin and end, so that directives are recognised only in comments,
// placeholders only in code, and statements only end at a real semicolon.
// This is the same set of constructs sqlc's own statement splitter honours.

type tokenKind uint8

const (
	tokCode         tokenKind = iota // everything else, including whitespace
	tokLineComment                   // -- ... or, on MySQL, # ...
	tokBlockComment                  // /* ... */, nested on PostgreSQL
	tokString                        // '...'; also "..." on MySQL
	tokQuoted                        // "..." identifier; `...` on MySQL and SQLite
	tokDollar                        // $tag$...$tag$ on PostgreSQL
	tokSemicolon                     // ;
)

type token struct {
	kind       tokenKind
	start, end int
}

// scanSQL tokenises src for the given engine. Unterminated constructs run to
// the end of the input.
func scanSQL(engine Engine, src []byte) []token {
	var toks []token
	n := len(src)
	codeStart := 0
	emit := func(kind tokenKind, start, end int) {
		if start > codeStart {
			toks = append(toks, token{tokCode, codeStart, start})
		}
		toks = append(toks, token{kind, start, end})
		codeStart = end
	}
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '-' && i+1 < n && src[i+1] == '-':
			end := lineEnd(src, i)
			emit(tokLineComment, i, end)
			i = end
		case c == '#' && engine == MySQL:
			end := lineEnd(src, i)
			emit(tokLineComment, i, end)
			i = end
		case c == '/' && i+1 < n && src[i+1] == '*':
			end := blockCommentEnd(src, i, engine == PostgreSQL)
			emit(tokBlockComment, i, end)
			i = end
		case c == '\'':
			backslash := engine == MySQL || (engine == PostgreSQL && hasEscapePrefix(src, i))
			end := quotedEnd(src, i, '\'', backslash)
			emit(tokString, i, end)
			i = end
		case c == '"':
			kind := tokQuoted
			if engine == MySQL {
				kind = tokString
			}
			end := quotedEnd(src, i, '"', engine == MySQL)
			emit(kind, i, end)
			i = end
		case c == '`' && engine != PostgreSQL:
			end := quotedEnd(src, i, '`', false)
			emit(tokQuoted, i, end)
			i = end
		case c == '$' && engine == PostgreSQL:
			if end, ok := dollarQuoteEnd(src, i); ok {
				emit(tokDollar, i, end)
				i = end
			} else {
				i++
			}
		case c == ';':
			emit(tokSemicolon, i, i+1)
			i++
		default:
			i++
		}
	}
	if n > codeStart {
		toks = append(toks, token{tokCode, codeStart, n})
	}
	return toks
}

// lineEnd returns the offset of the next newline at or after i, or len(src).
func lineEnd(src []byte, i int) int {
	if j := bytes.IndexByte(src[i:], '\n'); j >= 0 {
		return i + j
	}
	return len(src)
}

// lineEndInclusive returns the offset just past the next newline at or after
// i, or len(src).
func lineEndInclusive(src []byte, i int) int {
	end := lineEnd(src, i)
	if end < len(src) {
		end++
	}
	return end
}

func blockCommentEnd(src []byte, i int, nested bool) int {
	depth := 1
	for j := i + 2; j < len(src); {
		if nested && j+1 < len(src) && src[j] == '/' && src[j+1] == '*' {
			depth++
			j += 2
			continue
		}
		if j+1 < len(src) && src[j] == '*' && src[j+1] == '/' {
			depth--
			j += 2
			if depth == 0 {
				return j
			}
			continue
		}
		j++
	}
	return len(src)
}

// quotedEnd returns the offset just past the closing quote q of the literal
// that starts at i. A doubled quote continues the literal; so does a
// backslash-escaped character when backslash is set.
func quotedEnd(src []byte, i int, q byte, backslash bool) int {
	for j := i + 1; j < len(src); {
		switch {
		case backslash && src[j] == '\\':
			j += 2
		case src[j] == q:
			if j+1 < len(src) && src[j+1] == q {
				j += 2
				continue
			}
			return j + 1
		default:
			j++
		}
	}
	return len(src)
}

// hasEscapePrefix reports whether the quote at i is preceded by the E prefix
// of a PostgreSQL escape string literal.
func hasEscapePrefix(src []byte, i int) bool {
	if i == 0 || (src[i-1] != 'E' && src[i-1] != 'e') {
		return false
	}
	return i < 2 || !isIdentByte(src[i-2])
}

// dollarQuoteEnd recognises a PostgreSQL dollar-quoted string starting at i
// and returns the offset just past its closing tag. A $ followed by a digit
// is a positional parameter, not a quote.
func dollarQuoteEnd(src []byte, i int) (int, bool) {
	j := i + 1
	if j < len(src) && isDigit(src[j]) {
		return 0, false
	}
	for j < len(src) && isIdentByte(src[j]) {
		j++
	}
	if j >= len(src) || src[j] != '$' {
		return 0, false
	}
	tag := src[i : j+1]
	if k := bytes.Index(src[j+1:], tag); k >= 0 {
		return j + 1 + k + len(tag), true
	}
	return len(src), true
}

// inCode reports whether byte offset off lies in a code token of toks.
func inCode(toks []token, off int) bool {
	i := sort.Search(len(toks), func(i int) bool { return toks[i].end > off })
	return i < len(toks) && toks[i].kind == tokCode && toks[i].start <= off
}

// findMacros returns the sqlc parameter macros in text whose call starts in a
// code region. The quoted form sqlc.arg('name') spans a string token, which
// is why this is not a per-token search.
func findMacros(toks []token, text string) []macroMatch {
	var ms []macroMatch
	for _, m := range macroRE.FindAllStringSubmatchIndex(text, -1) {
		if inCode(toks, m[0]) {
			ms = append(ms, macroMatch{start: m[0], end: m[1], form: text[m[2]:m[3]], name: text[m[4]:m[5]]})
		}
	}
	return ms
}

type macroMatch struct {
	start, end int
	form, name string
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || isDigit(c)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
