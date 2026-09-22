package graph

import (
	"testing"
)

func TestLayersAndMaxWidth(t *testing.T) {
	b := NewBuilder()
	// Layer 0: a,b,c ; layer 1: d,e ; layer 2: f
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		if err := b.AddNode(id); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range [][2]string{{"a", "d"}, {"b", "d"}, {"b", "e"}, {"c", "e"}, {"d", "f"}, {"e", "f"}} {
		if err := b.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	g, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	layers := g.Layers()
	if len(layers) != 3 {
		t.Fatalf("want 3 layers, got %d: %v", len(layers), layers)
	}
	if len(layers[0]) != 3 || len(layers[1]) != 2 || len(layers[2]) != 1 {
		t.Fatalf("layer widths wrong: %v", layers)
	}
	if g.MaxWidth() != 3 {
		t.Fatalf("max width = %d, want 3", g.MaxWidth())
	}
	topo := g.TopoOrder()
	pos := map[string]int{}
	for i, id := range topo {
		pos[id] = i
	}
	for _, e := range [][2]string{{"a", "d"}, {"d", "f"}, {"c", "f"}} {
		if pos[e[0]] >= pos[e[1]] {
			t.Fatalf("topo violates edge %v: %v", e, topo)
		}
	}
}

func TestCycleReturnsRealClosedPath(t *testing.T) {
	b := NewBuilder()
	for _, id := range []string{"a", "b", "c", "d"} {
		_ = b.AddNode(id)
	}
	// cycle b -> c -> d -> b, with a feeding into b
	_ = b.AddEdge("a", "b")
	_ = b.AddEdge("b", "c")
	_ = b.AddEdge("c", "d")
	_ = b.AddEdge("d", "b")
	_, err := b.Build()
	ce, ok := AsCycle(err)
	if !ok {
		t.Fatalf("want CycleError, got %v", err)
	}
	p := ce.Path
	if len(p) < 3 || p[0] != p[len(p)-1] {
		t.Fatalf("path not closed: %v", p)
	}
	edge := map[string]map[string]bool{}
	for _, x := range []string{"a", "b", "c", "d"} {
		edge[x] = map[string]bool{}
	}
	edge["a"]["b"] = true
	edge["b"]["c"] = true
	edge["c"]["d"] = true
	edge["d"]["b"] = true
	for i := 0; i+1 < len(p); i++ {
		if !edge[p[i]][p[i+1]] {
			t.Fatalf("reported edge %s->%s not in input; path=%v", p[i], p[i+1], p)
		}
	}
}

func TestSelfEdge(t *testing.T) {
	b := NewBuilder()
	_ = b.AddNode("x")
	if err := b.AddEdge("x", "x"); err == nil {
		t.Fatal("self edge must be rejected")
	}
}

func TestReverseTopo(t *testing.T) {
	b := NewBuilder()
	for _, id := range []string{"A", "B", "C"} {
		_ = b.AddNode(id)
	}
	_ = b.AddEdge("A", "B")
	_ = b.AddEdge("B", "C")
	g, _ := b.Build()
	rt := g.ReverseTopoOrder()
	if len(rt) != 3 || rt[0] != "C" || rt[1] != "B" || rt[2] != "A" {
		t.Fatalf("reverse topo = %v, want [C B A]", rt)
	}
}
