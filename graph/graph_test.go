package graph

import (
	"errors"
	"testing"
)

func TestLayersAndOrders(t *testing.T) {
	b := NewBuilder()
	b.AddStep("A")
	b.AddStep("B")
	b.AddStep("C")
	b.AddStep("D")
	if err := b.AddEdge("A", "B"); err != nil {
		t.Fatal(err)
	}
	if err := b.AddEdge("A", "C"); err != nil {
		t.Fatal(err)
	}
	if err := b.AddEdge("B", "D"); err != nil {
		t.Fatal(err)
	}
	if err := b.AddEdge("C", "D"); err != nil {
		t.Fatal(err)
	}
	g, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	layers := g.Layers()
	if len(layers) != 3 {
		t.Fatalf("want 3 layers, got %d: %v", len(layers), layers)
	}
	if len(layers[0]) != 1 || layers[0][0] != "A" {
		t.Fatalf("layer0 = %v", layers[0])
	}
	if len(layers[1]) != 2 {
		t.Fatalf("layer1 = %v", layers[1])
	}
	if len(layers[2]) != 1 || layers[2][0] != "D" {
		t.Fatalf("layer2 = %v", layers[2])
	}
	top := g.TopoOrder()
	if top[0] != "A" || top[len(top)-1] != "D" {
		t.Fatalf("topo = %v", top)
	}
	rev := g.ReverseTopoOrder()
	if rev[0] != "D" || rev[len(rev)-1] != "A" {
		t.Fatalf("reverse = %v", rev)
	}
}

func TestCycleReportsRealClosedPath(t *testing.T) {
	b := NewBuilder()
	for _, id := range []string{"A", "B", "C", "D"} {
		b.AddStep(id)
	}
	if err := b.AddEdge("A", "B"); err != nil {
		t.Fatal(err)
	}
	if err := b.AddEdge("B", "C"); err != nil {
		t.Fatal(err)
	}
	if err := b.AddEdge("C", "A"); err != nil {
		t.Fatal(err)
	}
	if err := b.AddEdge("A", "D"); err != nil {
		t.Fatal(err)
	}
	_, err := b.Build()
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("want CycleError, got %v", err)
	}
	cyc := ce.Cycle
	if len(cyc) < 3 || cyc[0] != cyc[len(cyc)-1] {
		t.Fatalf("cycle not closed: %v", cyc)
	}
	edges := map[[2]string]bool{{"A", "B"}: true, {"B", "C"}: true, {"C", "A"}: true}
	for i := 0; i+1 < len(cyc); i++ {
		if !edges[[2]string{cyc[i], cyc[i+1]}] {
			t.Fatalf("cycle step %q->%q is not a real edge: %v", cyc[i], cyc[i+1], cyc)
		}
	}
}

func TestUnknownStepEdge(t *testing.T) {
	b := NewBuilder()
	b.AddStep("A")
	if err := b.AddEdge("A", "X"); !errors.Is(err, ErrUnknownStep) {
		t.Fatalf("want ErrUnknownStep, got %v", err)
	}
}
