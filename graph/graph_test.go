package graph

import "testing"

func TestBuildAndTargets(t *testing.T) {
	cases := []struct {
		name    string
		build   func() *Graph
		id      string
		want    []string
		wantHas bool
	}{
		{"sorted_edges", func() *Graph {
			b := NewBuilder()
			b.AddEdge("c", "z")
			b.AddEdge("c", "a")
			b.AddEdge("c", "m")
			return b.Build()
		}, "c", []string{"a", "m", "z"}, true},
		{"dedup_edges", func() *Graph {
			b := NewBuilder()
			b.AddEdge("x", "y")
			b.AddEdge("x", "y")
			return b.Build()
		}, "x", []string{"y"}, true},
		{"self_loop", func() *Graph {
			b := NewBuilder()
			b.AddEdge("s", "s")
			return b.Build()
		}, "s", []string{"s"}, true},
		{"isolated_target_registered", func() *Graph {
			b := NewBuilder()
			b.AddEdge("a", "b")
			return b.Build()
		}, "b", []string{}, true},
		{"missing_node", func() *Graph { return NewBuilder().Build() }, "nope", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.build()
			if got := g.Has(tc.id); got != tc.wantHas {
				t.Fatalf("Has(%q)=%v want %v", tc.id, got, tc.wantHas)
			}
			got := g.Targets(tc.id)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("Targets = %v want nil", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Targets = %v want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Targets[%d]=%q want %q (%v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func TestNodesSortedAndDelete(t *testing.T) {
	b := NewBuilder()
	b.AddEdge("a", "b")
	b.AddEdge("b", "c")
	b.AddEdge("c", "a")
	b.AddNode("d")
	g := b.Build()

	wantNodes := []string{"a", "b", "c", "d"}
	got := g.Nodes()
	if len(got) != len(wantNodes) {
		t.Fatalf("Nodes=%v want %v", got, wantNodes)
	}
	for i := range got {
		if got[i] != wantNodes[i] {
			t.Fatalf("Nodes not sorted: %v", got)
		}
	}

	g2 := g.Delete("c")
	if g2.Has("c") {
		t.Fatal("deleted node still present")
	}
	if g.Has("c") != true {
		t.Fatal("original graph must stay immutable")
	}
	if tg := g2.Targets("b"); len(tg) != 0 {
		t.Fatalf("edge to deleted node must be removed, got %v", tg)
	}
	if tg := g2.Targets("a"); len(tg) != 1 || tg[0] != "b" {
		t.Fatalf("unrelated edges must remain, got %v", tg)
	}
}
