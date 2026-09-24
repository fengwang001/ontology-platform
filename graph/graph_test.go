package graph_test

import (
	"errors"
	"testing"

	"ontology/graph"
)

func TestGraph(t *testing.T) {
	tests := []struct {
		name    string
		build   func(g *graph.Graph) error
		wantErr error
		wantCyc bool
		layers  [][]string
	}{
		{
			name:   "empty graph",
			build:  func(g *graph.Graph) error { return nil },
			layers: nil,
		},
		{
			name:   "single task",
			build:  func(g *graph.Graph) error { g.AddTask("a"); return nil },
			layers: [][]string{{"a"}},
		},
		{
			name: "all independent sorted",
			build: func(g *graph.Graph) error {
				g.AddTask("c")
				g.AddTask("a")
				g.AddTask("b")
				return nil
			},
			layers: [][]string{{"a", "b", "c"}},
		},
		{
			name: "diamond layers",
			build: func(g *graph.Graph) error {
				for _, id := range []string{"a", "b", "c", "d"} {
					g.AddTask(id)
				}
				for _, e := range [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}} {
					if err := g.AddEdge(e[0], e[1]); err != nil {
						return err
					}
				}
				return nil
			},
			layers: [][]string{{"a"}, {"b", "c"}, {"d"}},
		},
		{
			name: "duplicate edge idempotent",
			build: func(g *graph.Graph) error {
				g.AddTask("a")
				g.AddTask("b")
				if err := g.AddEdge("a", "b"); err != nil {
					return err
				}
				return g.AddEdge("a", "b")
			},
			layers: [][]string{{"a"}, {"b"}},
		},
		{
			name: "unknown dependency",
			build: func(g *graph.Graph) error {
				g.AddTask("a")
				return g.AddEdge("a", "ghost")
			},
			wantErr: graph.ErrUnknownDep,
		},
		{
			name: "self loop",
			build: func(g *graph.Graph) error {
				g.AddTask("a")
				return g.AddEdge("a", "a")
			},
			wantCyc: true,
		},
		{
			name: "multi node cycle",
			build: func(g *graph.Graph) error {
				for _, id := range []string{"a", "b", "c"} {
					g.AddTask(id)
				}
				for _, e := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}} {
					if err := g.AddEdge(e[0], e[1]); err != nil {
						return err
					}
				}
				return nil
			},
			wantCyc: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := graph.New()
			buildErr := tc.build(g)
			if tc.wantErr != nil {
				if !errors.Is(buildErr, tc.wantErr) {
					t.Fatalf("build error = %v, want %v", buildErr, tc.wantErr)
				}
				return
			}
			if buildErr != nil {
				t.Fatalf("unexpected build error: %v", buildErr)
			}
			err := g.Validate()
			var cyc *graph.CycleError
			gotCyc := errors.As(err, &cyc)
			if tc.wantCyc {
				if !gotCyc {
					t.Fatalf("expected CycleError, got %v", err)
				}
				if cyc.Path[0] != cyc.Path[len(cyc.Path)-1] {
					t.Fatalf("cycle path not closed: %v", cyc.Path)
				}
				edgeSet := map[string]bool{}
				for _, id := range g.IDs() {
					for _, s := range g.Succs(id) {
						edgeSet[id+"->"+s] = true
					}
				}
				for i := 0; i+1 < len(cyc.Path); i++ {
					if !edgeSet[cyc.Path[i]+"->"+cyc.Path[i+1]] {
						t.Fatalf("cycle uses edge not in input: %s->%s", cyc.Path[i], cyc.Path[i+1])
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected validate error: %v", err)
			}
			layers, err := g.Layers()
			if err != nil {
				t.Fatalf("layers: %v", err)
			}
			if len(layers) != len(tc.layers) {
				t.Fatalf("layers = %v, want %v", layers, tc.layers)
			}
			for i := range layers {
				if stringsJoin(layers[i]) != stringsJoin(tc.layers[i]) {
					t.Fatalf("layer %d = %v, want %v", i, layers[i], tc.layers[i])
				}
			}
		})
	}
}

// TestChain1000 ensures a deep chain is handled iteratively with no stack
// blow-up and yields 1000 layers.
func TestChain1000(t *testing.T) {
	g := graph.New()
	for i := 0; i < 1000; i++ {
		g.AddTask(idName(i))
	}
	for i := 1; i < 1000; i++ {
		if err := g.AddEdge(idName(i-1), idName(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	layers, err := g.Layers()
	if err != nil {
		t.Fatalf("layers: %v", err)
	}
	if len(layers) != 1000 {
		t.Fatalf("got %d layers, want 1000", len(layers))
	}
}

func stringsJoin(in []string) string {
	out := ""
	for _, s := range in {
		out += s + ","
	}
	return out
}

func idName(i int) string {
	// fixed width keeps lexicographic order aligned with numeric order
	return "t" + pad4(i)
}

func pad4(i int) string {
	digits := []byte{}
	for j := 0; j < 4; j++ {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
