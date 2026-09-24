package logical

import (
	"slices"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"comment-hash", "# x\nk=v", []string{"k=v"}},
		{"comment-bang", "! x\nk=v", []string{"k=v"}},
		{"comment-ws-prefix", "  # x\n\t! y\nk=v", []string{"k=v"}},
		{"blank-lines", "\n  \n\t\nk=v\n", []string{"k=v"}},
		{"leading-ws", "  k=v", []string{"k=v"}},
		{"cont-odd", "k=v\\\n   w", []string{"k=vw"}},
		{"cont-even", "k=v\\\\\nx=y", []string{`k=v\\`, "x=y"}},
		{"cont-odd3", "k=v\\\\\\\n  w", []string{`k=v\\w`}},
		{"cont-hash", "k=a\\\n#b", []string{"k=a#b"}},
		{"cont-ws-only", "k=a\\\n   \nb=c", []string{"k=a", "b=c"}},
		{"cont-empty-line", "k=a\\\n\nb=c", []string{"k=a", "b=c"}},
		{"cont-eof", "k=v\\", []string{"k=v"}},
		{"crlf", "a=1\r\nb=2\r\nc=3", []string{"a=1", "b=2", "c=3"}},
		{"cr-only", "a=1\rb=2", []string{"a=1", "b=2"}},
		{"comment-no-cont", "# c \\\nk=v", []string{"k=v"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var sc Scanner
			var got []string
			for _, l := range sc.Split([]byte(c.in)) {
				got = append(got, l.String())
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("Split(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSegmentPos(t *testing.T) {
	var sc Scanner
	lines := sc.Split([]byte("k=a\\\n  b\nx=y"))
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	text, starts := lines[0].Flatten()
	if text != "k=ab" {
		t.Fatalf("Flatten = %q, want %q", text, "k=ab")
	}
	cases := []struct{ off, line, col int }{
		{0, 1, 1}, {2, 1, 3}, {3, 2, 3},
	}
	for _, c := range cases {
		if l, co := lines[0].Pos(starts, c.off); l != c.line || co != c.col {
			t.Errorf("Pos(%d) = (%d,%d), want (%d,%d)", c.off, l, co, c.line, c.col)
		}
	}
	if sg := lines[1].Segs[0]; sg.Line != 3 || sg.Col != 1 {
		t.Errorf("seg = %+v, want {x=y 3 1}", sg)
	}
}

// 1 MB 长续行链：字节检查总次数 ≤ 2 × 输入字节数。
func TestCounterBound(t *testing.T) {
	var sb strings.Builder
	for sb.Len() < 1<<20 {
		sb.WriteString("key=some-long-value-chunk-abcdefghijklmnopqrstuvwxyz0123456789\\\n   ")
	}
	data := []byte(sb.String())
	var sc Scanner
	sc.Split(data)
	if got, limit := sc.Checked(), 2*len(data); got > limit {
		t.Errorf("checked %d > limit %d (input %d bytes)", got, limit, len(data))
	}
}
