package sqlcshare

import (
	"strings"
	"testing"
)

var tokenNames = map[tokenKind]string{
	tokCode: "code", tokLineComment: "line", tokBlockComment: "block", tokString: "string",
	tokQuoted: "quoted", tokDollar: "dollar", tokSemicolon: "semi",
}

func tokenList(engine Engine, src string) string {
	var parts []string
	for _, t := range scanSQL(engine, []byte(src)) {
		parts = append(parts, tokenNames[t.kind]+"⟨"+src[t.start:t.end]+"⟩")
	}
	return strings.Join(parts, " ")
}

func TestScan(t *testing.T) {
	tests := []struct {
		name   string
		engine Engine
		src    string
		want   string
	}{
		{
			name:   "postgresql strings, dollar quotes, nested comments, params",
			engine: PostgreSQL,
			src:    "SELECT 'it''s', E'a\\'b', \"q\"\"i\", $$x;y$$, $t$ ; $t$, $1 /* a /* n */ b */ -- c\n;",
			want: "code⟨SELECT ⟩ string⟨'it''s'⟩ code⟨, E⟩ string⟨'a\\'b'⟩ code⟨, ⟩ quoted⟨\"q\"\"i\"⟩ code⟨, ⟩ " +
				"dollar⟨$$x;y$$⟩ code⟨, ⟩ dollar⟨$t$ ; $t$⟩ code⟨, $1 ⟩ block⟨/* a /* n */ b */⟩ code⟨ ⟩ line⟨-- c⟩ code⟨\n⟩ semi⟨;⟩",
		},
		{
			name:   "postgresql backslash is literal outside E strings and backticks are code",
			engine: PostgreSQL,
			src:    "SELECT 'a\\', `x`;",
			want:   "code⟨SELECT ⟩ string⟨'a\\'⟩ code⟨, `x`⟩ semi⟨;⟩",
		},
		{
			name:   "mysql escapes, double-quoted strings, backticks, hash comments, flat comments",
			engine: MySQL,
			src:    "SELECT 'a\\'b', \"str\", `id`, # note\n/* a /* b */ c */;",
			want:   "code⟨SELECT ⟩ string⟨'a\\'b'⟩ code⟨, ⟩ string⟨\"str\"⟩ code⟨, ⟩ quoted⟨`id`⟩ code⟨, ⟩ line⟨# note⟩ code⟨\n⟩ block⟨/* a /* b */⟩ code⟨ c */⟩ semi⟨;⟩",
		},
		{
			name:   "sqlite has no backslash escapes and no hash comments",
			engine: SQLite,
			src:    "SELECT 'a\\', `x`, \"y\" # ;",
			want:   "code⟨SELECT ⟩ string⟨'a\\'⟩ code⟨, ⟩ quoted⟨`x`⟩ code⟨, ⟩ quoted⟨\"y\"⟩ code⟨ # ⟩ semi⟨;⟩",
		},
		{
			name:   "unterminated constructs run to the end",
			engine: PostgreSQL,
			src:    "SELECT 'abc",
			want:   "code⟨SELECT ⟩ string⟨'abc⟩",
		},
		{
			name:   "unterminated block comment",
			engine: MySQL,
			src:    "x /* y",
			want:   "code⟨x ⟩ block⟨/* y⟩",
		},
		{
			name:   "dollar followed by digit is a parameter",
			engine: PostgreSQL,
			src:    "WHERE a = $1 AND b = $2",
			want:   "code⟨WHERE a = $1 AND b = $2⟩",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tokenList(tt.engine, tt.src); got != tt.want {
				t.Errorf("\n got %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestLineIndex(t *testing.T) {
	li := newLineIndex([]byte("ab\ncd\n\nef"))
	for _, tt := range []struct{ off, line, col int }{{0, 1, 1}, {2, 1, 3}, {3, 2, 1}, {4, 2, 2}, {6, 3, 1}, {7, 4, 1}, {8, 4, 2}} {
		if line, col := li.lineCol(tt.off); line != tt.line || col != tt.col {
			t.Errorf("offset %d: got %d:%d, want %d:%d", tt.off, line, col, tt.line, tt.col)
		}
	}
	ts := newTextSource("f.sql", []byte("x\ny"), Pos{File: "f.sql", Line: 10, Col: 5})
	if got := ts.pos(0); got != (Pos{File: "f.sql", Line: 10, Col: 5}) {
		t.Errorf("base offset: got %v", got)
	}
	if got := ts.pos(2); got != (Pos{File: "f.sql", Line: 11, Col: 1}) {
		t.Errorf("second line: got %v", got)
	}
}
