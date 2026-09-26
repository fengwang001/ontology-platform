// White-box test: the queue-driven solver must never do a full-table sweep.
package sp

import (
	"testing"

	"ontology/dc"
)

// A negative chain x_{i+1} - x_i <= -1 needs only O(m) local relaxations;
// naive Bellman-Ford would need m full-table passes. The unexported
// fullPasses counter must stay 0 for every size.
func TestZeroFullPassesOnChain(t *testing.T) {
	for _, m := range []int{100, 500, 2000, 10000} {
		cons := make([]dc.Constraint, 0, m-1)
		for i := 0; i+1 < m; i++ {
			cons = append(cons, dc.Constraint{U: i, V: i + 1, W: -1})
		}
		eng := NewEngine(m, cons)
		got, err := eng.Solve()
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		for i, x := range got {
			if x != int64(-i) {
				t.Fatalf("m=%d: x[%d]=%d, want %d", m, i, x, -i)
			}
		}
		if eng.fullPasses != 0 {
			t.Fatalf("m=%d: fullPasses=%d, want 0 (queue-driven, no full sweeps)", m, eng.fullPasses)
		}
	}
}
