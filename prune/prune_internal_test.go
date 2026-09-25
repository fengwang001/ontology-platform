package prune

import (
	"fmt"
	"testing"

	"ontology/col"
)

// 不变量 2：保留集恰好是 refs 并集，未引用列被裁剪。
func TestPruneExactReadSet(t *testing.T) {
	s, err := col.NewSchema([]string{"a", "b", "c", "d", "e"})
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(s)
	if err := eng.SetProjection([]Output{
		{Out: "x", Refs: []string{"a"}},
		{Out: "sum", Refs: []string{"b", "c"}},
		{Out: "y", Refs: []string{"d"}},
	}); err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(eng.KeptCols())
	if got != "[a b c d]" {
		t.Fatalf("kept = %s, want [a b c d]", got)
	}
	for _, pruned := range []string{"e"} {
		for _, k := range eng.KeptCols() {
			if k == pruned {
				t.Fatalf("pruned column %q retained", pruned)
			}
		}
	}
}

// 复杂度约束：读取列数不随源表宽度 m 增长（内部测试直读非导出计数器）。
func TestReadCountIndependentOfWidth(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			cols := make([]string, m)
			row := map[string]int{}
			for i := range cols {
				cols[i] = fmt.Sprintf("c%d", i)
				row[cols[i]] = i
			}
			s, err := col.NewSchema(cols)
			if err != nil {
				t.Fatal(err)
			}
			eng := NewEngine(s)
			if err := eng.SetProjection([]Output{
				{Out: "p", Refs: []string{"c1"}},
				{Out: "q", Refs: []string{"c2", "c3"}},
			}); err != nil {
				t.Fatal(err)
			}
			if err := eng.Apply(row); err != nil {
				t.Fatal(err)
			}
			const want = 3 // 1 + 2 个被引用列，与 m 无关
			if eng.reads != want {
				t.Fatalf("reads = %d, want %d (must not grow with m=%d)", eng.reads, want, m)
			}
		})
	}
}
