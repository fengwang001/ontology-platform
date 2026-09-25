package hunk_test

import (
	"testing"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

type hdr struct {
	os, oc, ns, nc int
}

func build(a, b string, c int) []hunk.Hunk {
	ops, err := edit.Diff(lines.SplitString(a), lines.SplitString(b), 1<<30)
	if err != nil {
		panic(err)
	}
	return hunk.Build(ops, c)
}

func headers(hs []hunk.Hunk) []hdr {
	out := make([]hdr, len(hs))
	for i, h := range hs {
		out[i] = hdr{h.OldStart, h.OldCount, h.NewStart, h.NewCount}
	}
	return out
}

func TestZeroCountHeaders(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		c    int
		want hdr
	}{
		{"insert at top", "x\n", "y\nx\n", 0, hdr{0, 0, 1, 1}},
		{"insert middle", "a\nb\nc\n", "a\nb\nX\nc\n", 0, hdr{2, 0, 3, 1}},
		{"delete only", "a\n", "", 3, hdr{1, 1, 0, 0}},
	}
	for _, tc := range cases {
		hs := build(tc.a, tc.b, tc.c)
		if len(hs) != 1 {
			t.Fatalf("%s: got %d hunks", tc.name, len(hs))
		}
		got := headers(hs)[0]
		if got != tc.want {
			t.Errorf("%s: header = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestMergeThreshold2C(t *testing.T) {
	// 两处改动之间间隔 g 行（未改动行）。
	mk := func(g int) (string, string) {
		a := "A\n"
		for i := 0; i < g; i++ {
			a += "m\n"
		}
		a += "B\n"
		b := "X\n"
		for i := 0; i < g; i++ {
			b += "m\n"
		}
		b += "Y\n"
		return a, b
	}
	cases := []struct {
		c, g, want int
	}{
		{0, 0, 1}, // 阈值 2C=0：g=0 合并
		{0, 1, 2}, // g=1>0 拆分
		{1, 2, 1}, // 阈值 2C=2：g=2 恰好合并
		{1, 3, 2}, // g=3=2C+1 拆分
		{2, 4, 1}, // g=4=2C 合并
		{2, 5, 2}, // g=5 拆分
	}
	for _, tc := range cases {
		a, b := mk(tc.g)
		hs := build(a, b, tc.c)
		if len(hs) != tc.want {
			t.Errorf("C=%d g=%d: hunks=%d, want %d", tc.c, tc.g, len(hs), tc.want)
		}
	}
}

func TestNoChangeEmpty(t *testing.T) {
	if hs := build("a\nb\n", "a\nb\n", 3); len(hs) != 0 {
		t.Fatalf("identical input produced %d hunks", len(hs))
	}
}

func TestContextClamp(t *testing.T) {
	// 文件首行改动，上下文 C=3 向下扩展后覆盖 4 行（第 5 行不在 hunk 内）。
	hs := build("a\nb\nc\nd\ne\n", "X\nb\nc\nd\ne\n", 3)
	if len(hs) != 1 {
		t.Fatalf("hunks=%d", len(hs))
	}
	h := hs[0]
	if h.OldStart != 1 || h.OldCount != 4 || h.NewStart != 1 || h.NewCount != 4 {
		t.Fatalf("header = %d,%d %d,%d", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
	}
}
