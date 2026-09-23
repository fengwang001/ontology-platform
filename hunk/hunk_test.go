package hunk

import (
	"strings"
	"testing"

	"ontology/edit"
	"ontology/lines"
)

func build(a, b string, c int) []Hunk {
	la, lb := lines.Split([]byte(a)), lines.Split([]byte(b))
	ops, err := edit.Diff(la, lb, -1)
	if err != nil {
		panic(err)
	}
	return Build(la, lb, ops, c)
}

func TestZeroCountHeaders(t *testing.T) {
	cases := []struct {
		a, b   string
		c      int
		os, ol int
		ns, nl int
	}{
		{"x\n", "y\nx\n", 0, 0, 0, 1, 1},
		{"a\nb\nc\n", "a\nb\nX\nc\n", 0, 2, 0, 3, 1},
		{"a\n", "", 3, 1, 1, 0, 0},
	}
	for _, tc := range cases {
		hs := build(tc.a, tc.b, tc.c)
		if len(hs) != 1 {
			t.Fatalf("%q->%q: %d hunks", tc.a, tc.b, len(hs))
		}
		h := hs[0]
		if h.OldStart != tc.os || h.OldLen != tc.ol ||
			h.NewStart != tc.ns || h.NewLen != tc.nl {
			t.Fatalf("got -%d,%d +%d,%d want -%d,%d +%d,%d",
				h.OldStart, h.OldLen, h.NewStart, h.NewLen,
				tc.os, tc.ol, tc.ns, tc.nl)
		}
	}
}

func TestMergeThreshold(t *testing.T) {
	// C=1：两处改动间 g 个未改行行；g<=2 合并（1 个 hunk），g=3 分开（2 个）。
	cases := []struct {
		g     int
		hunks int
	}{
		{2, 1}, // 恰好阈值 2C
		{3, 2}, // 阈值+1
		{0, 1},
	}
	for _, tc := range cases {
		a := "A\n" + strings.Repeat("m\n", tc.g) + "B\n"
		b := "A1\n" + strings.Repeat("m\n", tc.g) + "B1\n"
		hs := build(a, b, 1)
		if len(hs) != tc.hunks {
			t.Fatalf("g=%d: got %d hunks want %d", tc.g, len(hs), tc.hunks)
		}
	}
}

func TestCRLFPreservedInRows(t *testing.T) {
	hs := build("a\r\nb\r\n", "a\r\nB\r\n", 3)
	if len(hs) != 1 {
		t.Fatalf("hunks=%d", len(hs))
	}
	for _, r := range hs[0].Rows {
		if r.Line.Term != "\r\n" {
			t.Fatalf("term lost: %+v", r)
		}
	}
}

func TestNoChangeNoHunk(t *testing.T) {
	if hs := build("x\ny\n", "x\ny\n", 3); len(hs) != 0 {
		t.Fatalf("want 0 hunks, got %d", len(hs))
	}
}
