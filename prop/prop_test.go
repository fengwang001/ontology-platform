package prop

import (
	"fmt"
	"testing"

	"ontology/dag"
)

// 在七节点图外再挂 m 个与 A 无关的节点（另一条源上的长链），
// Apply{A:5} 的检查数不得随 m 线性增长。
func TestCheckedIndependentOfUnrelatedSize(t *testing.T) {
	base := []dag.Def{
		{Name: "A", Kind: "src", K: 1}, {Name: "E", Kind: "src", K: 100},
		{Name: "B", Kind: "scale", Inputs: []string{"A"}, K: 2},
		{Name: "C", Kind: "add", Inputs: []string{"A"}, K: 10},
		{Name: "D", Kind: "sum", Inputs: []string{"B", "C"}},
		{Name: "F", Kind: "sum", Inputs: []string{"D", "E"}},
		{Name: "G", Kind: "sum", Inputs: []string{"A", "D"}},
	}
	for _, m := range []int{100, 1000, 10000} {
		defs := append(append([]dag.Def{}, base...), dag.Def{Name: "S", Kind: "src"})
		prev := "S"
		for i := 0; i < m; i++ {
			n := fmt.Sprintf("L%d", i)
			defs = append(defs, dag.Def{Name: n, Kind: "add", Inputs: []string{prev}, K: 1})
			prev = n
		}
		g, err := dag.New(defs)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		e := New(g)
		log, err := e.Apply(map[string]int64{"A": 5})
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if len(log) != 6 {
			t.Fatalf("m=%d: log len = %d, want 6", m, len(log))
		}
		if e.checked > 40 {
			t.Fatalf("m=%d: checked = %d, want <= 40 (grows with m)", m, e.checked)
		}
	}
}
