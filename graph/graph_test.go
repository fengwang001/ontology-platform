package graph

import (
	"errors"
	"testing"
)

func TestLayersAndWidth(t *testing.T) {
	g := New()
	ids := []string{"a", "b", "c", "d", "e", "f"}
	for _, id := range ids {
		if err := g.AddNode(id); err != nil {
			t.Fatal(err)
		}
	}
	mustEdge := func(a, b string) {
		t.Helper()
		if err := g.AddEdge(a, b); err != nil {
			t.Fatal(err)
		}
	}
	mustEdge("a", "d")
	mustEdge("b", "d")
	mustEdge("c", "d")
	mustEdge("d", "e")
	mustEdge("d", "f")
	layers, err := g.Layers()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"a", "b", "c"}, {"d"}, {"e", "f"}}
	if len(layers) != len(want) {
		t.Fatalf("got %d layers, want %d", len(layers), len(want))
	}
	for i := range want {
		if len(layers[i]) != len(want[i]) {
			t.Fatalf("layer %d width = %d, want %d", i, len(layers[i]), len(want[i]))
		}
	}
}

func TestCycleReportsRealClosedPath(t *testing.T) {
	g := New()
	for _, id := range []string{"a", "b", "c", "x"} {
		_ = g.AddNode(id)
	}
	_ = g.AddEdge("a", "b")
	_ = g.AddEdge("b", "c")
	_ = g.AddEdge("c", "a")
	err := g.Validate()
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("want CycleError, got %v", err)
	}
	if ce.Path[0] != ce.Path[len(ce.Path)-1] {
		t.Fatalf("path not closed: %v", ce.Path)
	}
	if err := g.CheckCyclePath(ce.Path); err != nil {
		t.Fatalf("reported path is not a real cycle: %v", err)
	}
}

func TestUnknownAndDuplicate(t *testing.T) {
	g := New()
	if err := g.AddNode("a"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode("a"); !errors.Is(err, ErrDuplicateNode) {
		t.Fatalf("got %v", err)
	}
	if err := g.AddEdge("a", "z"); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("got %v", err)
	}
}
