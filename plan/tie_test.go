package plan

import (
	"testing"

	"ontology/cost"
	"ontology/stats"
)

// Star A-B, A-C with rows(B)=2, NDV(AB)=3, rows(C)=4, NDV(AC)=12: the plans
// (A join B) join C and (A join C) join B have identical real-valued costs
// (both 5666672+2/3), but naive float64 accumulation differs by one ULP
// (5666672.666666666 vs 5666672.666666667). The DP must still
// deterministically pick the plan with the lexicographically smaller
// leaf-name sequence.
func TestTieBreakByName(t *testing.T) {
	tabs := map[string]tabSpec{
		"A": {1000000, map[string]float64{"ab": 3, "ac": 12}},
		"B": {2, map[string]float64{"b": 3}},
		"C": {4, map[string]float64{"c": 12}},
	}
	preds := [][4]string{{"A", "ab", "B", "b"}, {"A", "ac", "C", "c"}}
	root, _, err := Optimize(mkCatalog(t, tabs, preds), []string{"A", "B", "C"})
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Key(); got[0] != "A" || got[1] != "B" || got[2] != "C" {
		t.Fatalf("winner key = %v, want [A B C]", got)
	}

	// Recompute both orders by hand to prove they differ in float64 yet are
	// treated as equal by costEqual.
	a := &stats.Table{Name: "A", Rows: 1000000, Columns: map[string]stats.Column{
		"ab": {NDV: 3}, "ac": {NDV: 12}}}
	b := &stats.Table{Name: "B", Rows: 2, Columns: map[string]stats.Column{"b": {NDV: 3}}}
	c := &stats.Table{Name: "C", Rows: 4, Columns: map[string]stats.Column{"c": {NDV: 12}}}
	selAB := stats.EqSelectivity(a, "ab", b, "b").Sel
	selAC := stats.EqSelectivity(a, "ac", c, "c").Sel
	cardAB := cost.Card(1000000, 2, selAB)
	cardAC := cost.Card(1000000, 4, selAC)
	p1 := cost.Join(cardAB, 4, cost.Join(1000000, 2, 1000000, 2), 4)
	p2 := cost.Join(cardAC, 2, cost.Join(1000000, 4, 1000000, 4), 2)
	if p1 == p2 {
		t.Fatal("expected naive accumulation to differ by ULPs")
	}
	if !costEqual(p1, p2) {
		t.Fatalf("costEqual(%v, %v) = false", p1, p2)
	}
}
