package sqlcshare

import (
	"fmt"
	"regexp"
	"strings"
)

// A directive is a block comment whose text starts with a percent sign:
//
//	/*%where visible_author(a)*/ TRUE /*%end*/
//	/*%cte recent_orders*/
//
// The first form has fallback text; the second is replaced in place. A
// fallback must be closed by /*%end*/ before the next directive.
type directive struct {
	end  bool
	kind Kind
	name string
	args []string
}

var directiveRE = regexp.MustCompile(`^%\s*([A-Za-z]+)(?:\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:\(([^)]*)\))?)?\s*$`)

// commentInner strips the comment delimiters from a block comment.
func commentInner(text string) string {
	text = strings.TrimPrefix(text, "/*")
	text = strings.TrimSuffix(text, "*/")
	return strings.TrimSpace(text)
}

// isDirectiveComment reports whether a block comment is a directive, without
// validating it.
func isDirectiveComment(text string) bool {
	return strings.HasPrefix(commentInner(text), "%")
}

// parseDirective parses a block comment. ok is false when the comment is not a
// directive at all; err reports a malformed directive.
func parseDirective(text string) (d *directive, ok bool, err error) {
	inner := commentInner(text)
	if !strings.HasPrefix(inner, "%") {
		return nil, false, nil
	}
	m := directiveRE.FindStringSubmatch(inner)
	if m == nil {
		return nil, true, fmt.Errorf("malformed directive %s (want /*%%kind name(args)*/ or /*%%end*/)", text)
	}
	word := strings.ToLower(m[1])
	if word == "end" {
		if m[2] != "" {
			return nil, true, fmt.Errorf("/*%%end*/ takes no fragment name")
		}
		return &directive{end: true}, true, nil
	}
	kind, known := ParseKind(word)
	if !known {
		return nil, true, fmt.Errorf("unknown directive %%%s (want columns, where, joins, cte, sql or end)", m[1])
	}
	if m[2] == "" {
		return nil, true, fmt.Errorf("directive %%%s needs a fragment name", word)
	}
	args, err := parseIdentList(m[3])
	if err != nil {
		return nil, true, fmt.Errorf("directive %%%s %s: %v", word, m[2], err)
	}
	return &directive{kind: kind, name: m[2], args: args}, true, nil
}

var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseIdentList parses a comma-separated list of identifiers. An empty or
// blank list is allowed.
func parseIdentList(list string) ([]string, error) {
	if strings.TrimSpace(list) == "" {
		return nil, nil
	}
	parts := strings.Split(list, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if !identRE.MatchString(p) {
			return nil, fmt.Errorf("%q is not an identifier", p)
		}
		out = append(out, p)
	}
	return out, nil
}
