package graph

import (
	"reflect"
	"testing"
)

func TestGraph(t *testing.T) {
	cases := []struct {
		name  string
		build func(*Builder)
		nodes []string
		adj   map[string][]string
		has   map[string]bool
	}{
		{
			name:  "empty",
			build: func(*Builder) {},
			nodes: nil,
			adj:   map[string][]string{},
			has:   map[string]bool{"x": false},
		},
		{
			name: "sorted dedup selfloop",
			build: func(b *Builder) {
				b.AddEdge("a", "c")
				b.AddEdge("a", "b")
				b.AddEdge("a", "b")
				b.AddEdge("a", "a")
			},
			nodes: []string{"a", "b", "c"},
			adj:   map[string][]string{"a": {"a", "b", "c"}},
			has:   map[string]bool{"a": true, "z": false},
		},
		{
			name: "isolated and unreachable nodes",
			build: func(b *Builder) {
				b.AddNode("iso")
				b.AddEdge("x", "y")
			},
			nodes: []string{"iso", "x", "y"},
			adj:   map[string][]string{"x": {"y"}, "iso": nil},
			has:   map[string]bool{"iso": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuilder()
			tc.build(b)
			g := b.Build()
			if got := g.Nodes(); !reflect.DeepEqual(got, tc.nodes) {
				t.Fatalf("nodes = %v, want %v", got, tc.nodes)
			}
			for from, want := range tc.adj {
				if got := g.Neighbors(from); !reflect.DeepEqual(got, want) {
					t.Fatalf("adj[%q] = %v, want %v", from, got, want)
				}
			}
			for id, want := range tc.has {
				if g.Has(id) != want {
					t.Fatalf("has(%q) = %v, want %v", id, !want, want)
				}
			}
		})
	}
}

func TestOutDegree(t *testing.T) {
	cases := []struct {
		id   string
		want int
	}{
		{"a", 3},
		{"missing", 0},
		{"iso", 0},
	}
	b := NewBuilder()
	b.AddEdge("a", "b")
	b.AddEdge("a", "c")
	b.AddEdge("a", "d")
	b.AddNode("iso")
	g := b.Build()
	for _, tc := range cases {
		if got := g.OutDegree(tc.id); got != tc.want {
			t.Fatalf("outdegree(%q) = %d, want %d", tc.id, got, tc.want)
		}
	}
}
