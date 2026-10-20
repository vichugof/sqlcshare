package sqlcshare

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
)

// SegmentKind says where a piece of output text came from.
type SegmentKind uint8

const (
	segUnset SegmentKind = iota
	// SegSource is text copied from the query source file.
	SegSource
	// SegFragment is text copied from a fragment body.
	SegFragment
	// SegGenerated is text the expander wrote: provenance comments, the
	// marker, and the wrapping of where and cte fragments. Its Src is the
	// directive or statement it belongs to.
	SegGenerated
)

// Segment maps an output position to the position its text came from. The
// segment extends to the start of the next segment; lines after the first are
// copied whole, so an output column on them is the source column.
type Segment struct {
	OutLine, OutCol int
	Src             Pos
	Kind            SegmentKind
	Fragment        string // fragment name for SegFragment and SegGenerated
	Derived         string // name of the derived statement the text belongs to, if any
}

// SourceMap maps positions in an expanded file back to the source and
// fragment files.
type SourceMap struct {
	Source   string // source file name as given to Expand
	Out      string // output path, set by Generate
	Segments []Segment
}

// Origin maps an output position to its origin. col is clamped to 1 because
// some sqlc diagnostics report column 0.
func (m *SourceMap) Origin(line, col int) (Pos, *Segment, bool) {
	if m == nil || len(m.Segments) == 0 || line < 1 {
		return Pos{}, nil, false
	}
	if col < 1 {
		col = 1
	}
	i := sort.Search(len(m.Segments), func(i int) bool {
		s := m.Segments[i]
		return s.OutLine > line || (s.OutLine == line && s.OutCol > col)
	}) - 1
	if i < 0 {
		return Pos{}, nil, false
	}
	s := &m.Segments[i]
	p := s.Src
	if s.OutLine == line {
		p.Col = s.Src.Col + (col - s.OutCol)
	} else {
		p.Line = s.Src.Line + (line - s.OutLine)
		p.Col = col
	}
	return p, s, true
}

var sqlcDiagRE = regexp.MustCompile(`^(\S+\.sql):(\d+):(\d+): (.*)$`)

// RewriteDiagnostics copies sqlc's output from r to w, rewriting every
// `path:line:col: message` line whose path is a key of maps to the position
// in the source or fragment file it came from. Other lines pass through. An
// error inside a fragment is reported by sqlc once per query that uses the
// fragment; those repeats are collapsed into one line.
func RewriteDiagnostics(r io.Reader, w io.Writer, maps map[string]*SourceMap) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	type entry struct {
		text  string // rewritten line, or the original when it is not a diagnostic
		key   string // origin and message, for collapsing repeats
		where string // first expansion site
		more  int    // further expansion sites
	}
	var entries []*entry
	index := map[string]*entry{}
	for sc.Scan() {
		line := sc.Text()
		d := rewriteDiagnostic(line, maps)
		if d == nil {
			entries = append(entries, &entry{text: line})
			continue
		}
		if prev := index[d.key]; prev != nil {
			prev.more++
			continue
		}
		e := &entry{text: d.text, key: d.key, where: d.where}
		index[d.key] = e
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	for _, e := range entries {
		text := e.text
		if e.key != "" {
			text += " (" + e.where
			if e.more > 0 {
				text += fmt.Sprintf(" and %d more", e.more)
			}
			text += ")"
		}
		if _, err := io.WriteString(w, text+"\n"); err != nil {
			return err
		}
	}
	return nil
}

type rewritten struct {
	text, key, where string
}

// rewriteDiagnostic maps one sqlc diagnostic line; it returns nil when the
// line is not a diagnostic for a generated file.
func rewriteDiagnostic(line string, maps map[string]*SourceMap) *rewritten {
	m := sqlcDiagRE.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	sm := maps[m[1]]
	if sm == nil {
		return nil
	}
	l, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	origin, seg, ok := sm.Origin(l, c)
	if !ok {
		return nil
	}
	text := origin.String() + ": " + m[4]
	where := fmt.Sprintf("expanded at %s:%d:%d", m[1], l, c)
	if seg.Kind == SegFragment {
		where = "in fragment " + seg.Fragment + ", " + where
	}
	if seg.Derived != "" {
		where = "in derived query " + seg.Derived + ", " + where
	}
	return &rewritten{text: text, key: text, where: where}
}
