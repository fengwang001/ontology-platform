package prune

import (
	"fmt"
	"testing"

	"ontology/col"
)

// 不变量2：裁剪后读取的源列数只取决于 refs 并集，不随表宽 m 线性增长。
// 计数器 lastRead 是非导出字段，只能在包内核验。
func TestPrunedReadsIndependentOfWidth(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		names := make([]string, m)
		row := make(map[string]int, m)
		for i := range names {
			names[i] = fmt.Sprintf("c%05d", i)
			row[names[i]] = i
		}
		s, err := col.New(names)
		if err != nil {
			t.Fatalf("m=%d schema: %v", m, err)
		}
		p, err := New(s, []Def{
			{Out: "o1", Refs: []string{names[0]}},
			{Out: "o2", Refs: []string{names[42]}},
		})
		if err != nil {
			t.Fatalf("m=%d projection: %v", m, err)
		}
		out := p.Project(row)
		if p.lastRead != 2 {
			t.Errorf("m=%d: read %d source columns, want exactly 2", m, p.lastRead)
		}
		if len(p.retained) != 2 || out[0] != 0 || out[1] != 42 {
			t.Errorf("m=%d: retained=%v out=%v, want [c0,c42] [0 42]", m, p.retained, out)
		}
	}
}

// 不变量3：输出顺序恒为定义顺序、名称为 Out；保留集按源列模式序去重排列。
func TestOutputOrderAndNames(t *testing.T) {
	cases := []struct {
		cols     []string
		defs     []Def
		names    []string
		retained []string
		row      map[string]int
		out      []int
	}{
		{
			[]string{"a", "b", "c", "d", "e"},
			[]Def{{Out: "x", Refs: []string{"a"}}, {Out: "sum", Refs: []string{"b", "c"}}, {Out: "y", Refs: []string{"d"}}},
			[]string{"x", "sum", "y"}, []string{"a", "b", "c", "d"},
			map[string]int{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5}, []int{1, 5, 4},
		},
		{ // 引用顺序与 schema 序不一致、且同一源列被两个输出列复用。
			[]string{"z", "a", "b"},
			[]Def{{Out: "p", Refs: []string{"b", "a"}}, {Out: "q", Refs: []string{"a"}}},
			[]string{"p", "q"}, []string{"a", "b"},
			map[string]int{"z": 9, "a": 4, "b": 7}, []int{11, 4},
		},
	}
	for i, tc := range cases {
		s, err := col.New(tc.cols)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		p, err := New(s, tc.defs)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got := p.Names(); !eqStr(got, tc.names) {
			t.Errorf("case %d names=%v want %v", i, got, tc.names)
		}
		if got := p.Retained(); !eqStr(got, tc.retained) {
			t.Errorf("case %d retained=%v want %v", i, got, tc.retained)
		}
		if got := p.Project(tc.row); !eqInt(got, tc.out) {
			t.Errorf("case %d out=%v want %v", i, got, tc.out)
		}
	}
}

// 不变量1：逐输出列结果必须等于从完整行出发、按 refs 求和的全量重算。
func TestViewMatchesFullRecompute(t *testing.T) {
	s, _ := col.New([]string{"a", "b", "c", "d", "e"})
	defs := []Def{
		{Out: "x", Refs: []string{"a"}},
		{Out: "sum", Refs: []string{"b", "c"}},
		{Out: "y", Refs: []string{"d"}},
	}
	p, err := New(s, defs)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 6; n++ { // 多行循环：值随行变化，逼迫逐行现算。
		row := map[string]int{"a": n, "b": n + 1, "c": n + 2, "d": n + 3, "e": n + 4}
		full := make([]int, len(defs))
		for j, d := range defs { // 全量重算：不依赖任何裁剪信息。
			for _, r := range d.Refs {
				full[j] += row[r]
			}
		}
		if got := p.Project(row); !eqInt(got, full) {
			t.Errorf("row %d: projected=%v full=%v", n, got, full)
		}
	}
}

func eqStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func eqInt(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
