package graph

import (
	"errors"
	"testing"
)

func TestGraphTable(t *testing.T) {
	// 1) layered scheduling: width-3 middle layer and cycle rejection.
	ids := []string{"a", "b", "c", "d", "e"}
	// a,b are roots; c,d depend on a,b; e depends on c,d.
	edges := [][2]string{{"a", "c"}, {"b", "c"}, {"a", "d"}, {"b", "d"}, {"c", "e"}, {"d", "e"}}
	g, err := New(ids, edges)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	layers := g.Layers()
	wantWidth := []int{2, 2, 1}
	if len(layers) != len(wantWidth) {
		t.Fatalf("layers = %v", layers)
	}
	for i, w := range wantWidth {
		if len(layers[i]) != w {
			t.Fatalf("layer %d width = %d, want %d (%v)", i, len(layers[i]), w, layers)
		}
	}
	if g.InDegree("c") != 2 || g.InDegree("a") != 0 {
		t.Fatalf("indegree wrong: %d %d", g.InDegree("c"), g.InDegree("a"))
	}

	// 2) cycle reports a real, closed path that truly exists in the input.
	cyc := [][2]string{{"a", "b"}, {"b", "c"}, {"c", "b"}, {"c", "d"}}
	_, err = New([]string{"a", "b", "c", "d"}, cyc)
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("want CycleError, got %v", err)
	}
	p := ce.Path
	if len(p) < 2 || p[0] != p[len(p)-1] {
		t.Fatalf("path not closed: %v", p)
	}
	edgeSet := map[[2]string]bool{}
	for _, e := range cyc {
		edgeSet[e] = true
	}
	for i := 0; i+1 < len(p); i++ {
		if !edgeSet[[2]string{p[i], p[i+1]}] {
			t.Fatalf("reported edge %s->%s not in input; path=%v", p[i], p[i+1], p)
		}
	}

	// 3) reverse order respects layers (later layers compensate first).
	g2, _ := New([]string{"A", "B", "C"}, [][2]string{{"A", "B"}, {"B", "C"}})
	ro := g2.ReverseOrder()
	if len(ro) != 3 || ro[0] != "C" || ro[1] != "B" || ro[2] != "A" {
		t.Fatalf("reverse order = %v, want [C B A]", ro)
	}

	// 4) unknown edge / duplicate detection.
	if _, err := New([]string{"x"}, [][2]string{{"x", "y"}}); !errors.Is(err, ErrMissing) {
		t.Fatalf("want ErrMissing, got %v", err)
	}
	if _, err := New([]string{"x", "x"}, nil); err == nil {
		t.Fatal("duplicate id accepted")
	}
}
