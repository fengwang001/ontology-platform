package graph

import (
	"errors"
	"reflect"
	"testing"
)

func build(t *testing.T, nodes []string, edges [][2]string) *Graph {
	t.Helper()
	g := New()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n, err)
		}
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatalf("AddEdge(%s,%s): %v", e[0], e[1], err)
		}
	}
	return g
}

func TestCycleRejectedWithRealClosedPath(t *testing.T) {
	edges := [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}, {"x", "a"}}
	g := build(t, []string{"x", "a", "b", "c"}, edges)
	err := g.Validate()
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("want CycleError, got %v", err)
	}
	p := ce.Path
	if len(p) < 2 || p[0] != p[len(p)-1] {
		t.Fatalf("path not closed: %v", p)
	}
	edgeSet := map[[2]string]bool{}
	for _, e := range edges {
		edgeSet[e] = true
	}
	for i := 0; i+1 < len(p); i++ {
		if !edgeSet[[2]string{p[i], p[i+1]}] {
			t.Fatalf("edge %s->%s of reported cycle not in input", p[i], p[i+1])
		}
	}
}

func TestSelfLoopIsCycle(t *testing.T) {
	g := build(t, []string{"a"}, nil)
	var ce *CycleError
	if err := g.AddEdge("a", "a"); !errors.As(err, &ce) {
		t.Fatalf("want CycleError, got %v", err)
	}
}

func TestDuplicateAndUnknownRejected(t *testing.T) {
	g := build(t, []string{"a"}, nil)
	if err := g.AddNode("a"); !errors.Is(err, ErrDuplicateNode) {
		t.Fatalf("want ErrDuplicateNode, got %v", err)
	}
	if err := g.AddEdge("a", "b"); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("want ErrUnknownNode, got %v", err)
	}
}

func TestLayersAndTopoOrder(t *testing.T) {
	g := build(t, []string{"a", "b", "c", "d"},
		[][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}})
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"a"}, {"b", "c"}, {"d"}}
	if got := g.Layers(); !reflect.DeepEqual(got, want) {
		t.Fatalf("layers = %v, want %v", got, want)
	}
	if got := g.TopoOrder(); !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("topo = %v", got)
	}
	if got := g.ReverseTopoOrder(); !reflect.DeepEqual(got, []string{"d", "c", "b", "a"}) {
		t.Fatalf("reverse topo = %v", got)
	}
}

func TestWideLayerWidth(t *testing.T) {
	nodes := []string{"root", "w1", "w2", "w3", "w4", "w5"}
	g := build(t, nodes, [][2]string{
		{"root", "w1"}, {"root", "w2"}, {"root", "w3"}, {"root", "w4"}, {"root", "w5"},
	})
	layers := g.Layers()
	if len(layers) != 2 || len(layers[1]) != 5 {
		t.Fatalf("layers = %v", layers)
	}
}
