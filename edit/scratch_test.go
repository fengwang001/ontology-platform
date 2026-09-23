package edit

import (
	"ontology/lines"
	"testing"
)

func ls(s string) []lines.Line { return lines.Split(s) }

func TestScratch(t *testing.T) {
	cases := [][2]string{
		{"a\nb\n", "b\na\n"},
		{"x\n", "y\nx\n"},
		{"a\nb\nc\n", "a\nb\nX\nc\n"},
		{"a\n", ""},
		{"", "a\n"},
		{"abc", "abc"},
		{"a\r\nb\r\n", "a\r\nc\r\nb\r\n"},
		{"foo\n", "foo"},
	}
	for _, c := range cases {
		s, err := Diff(ls(c[0]), ls(c[1]), 0)
		if err != nil {
			t.Fatalf("%q->%q err %v", c[0], c[1], err)
		}
		t.Logf("%q->%q dist=%d ops=%v", c[0], c[1], s.Dist, s.Ops)
	}
}
