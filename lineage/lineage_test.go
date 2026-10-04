package lineage

import (
	"reflect"
	"testing"
)

func TestKindApply(t *testing.T) {
	cases := []struct {
		kind Kind
		x    int
		want int
	}{
		{Copy, 3, 3},
		{Mask, 4, 3}, {Mask, 1, 0}, {Mask, 0, 0},
		{Hash, 4, 2}, {Hash, 2, 0}, {Hash, 0, 0},
		{Agg, 4, 2}, {Agg, 1, 1}, {Agg, 2, 2},
	}
	for _, tc := range cases {
		if got := tc.kind.Apply(tc.x); got != tc.want {
			t.Errorf("%v.Apply(%d) = %d, want %d", tc.kind, tc.x, got, tc.want)
		}
	}
}

func TestGraphEdgesAndReaches(t *testing.T) {
	g := NewGraph()
	if g.HasEdge("a", "b") {
		t.Fatal("empty graph has no edge")
	}
	if !g.Add("a", "b", Mask) {
		t.Fatal("add edge")
	}
	if g.Add("a", "b", Hash) {
		t.Fatal("same pair duplicate rejected")
	}
	g.Add("b", "c", Hash)
	g.Add("a", "d", Agg)
	if k, _ := g.KindOf("a", "b"); k != Mask {
		t.Fatalf("kind = %v", k)
	}
	if g.InDegree("c") != 1 || g.OutDegree("a") != 2 {
		t.Fatal("degree mismatch")
	}
	if !g.Reaches("a", "c") || g.Reaches("c", "a") || g.Reaches("a", "a") {
		t.Fatal("Reaches mismatch")
	}
	if got := g.InEdges("c"); !reflect.DeepEqual(got, []Edge{{Src: "b", Dst: "c", Kind: Hash}}) {
		t.Fatalf("InEdges = %v", got)
	}
	if got := g.OutNeighbors("a"); !reflect.DeepEqual(got, []string{"b", "d"}) {
		t.Fatalf("OutNeighbors = %v", got)
	}
	if !g.Remove("a", "b") || g.Remove("a", "b") {
		t.Fatal("remove existing once, then missing")
	}
	if g.Reaches("a", "c") {
		t.Fatal("reachability after remove")
	}
}
