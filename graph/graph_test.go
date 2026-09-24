package graph

import (
	"reflect"
	"testing"
)

func TestAdjacencySortedAndDeduped(t *testing.T) {
	cases := []struct {
		name  string
		edges [][2]string
		from  string
		want  []string
	}{
		{"insertion order ignored", [][2]string{{"a", "c"}, {"a", "b"}, {"a", "d"}}, "a", []string{"b", "c", "d"}},
		{"duplicate edges collapse", [][2]string{{"a", "b"}, {"a", "b"}, {"a", "b"}}, "a", []string{"b"}},
		{"self loop kept once", [][2]string{{"a", "a"}, {"a", "a"}}, "a", []string{"a"}},
		{"mixed", [][2]string{{"a", "z"}, {"a", "a"}, {"a", "m"}, {"a", "z"}}, "a", []string{"a", "m", "z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := New()
			for _, e := range tc.edges {
				g.AddEdge(e[0], e[1])
			}
			if got := g.Out(tc.from); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Out(%q) = %v, want %v", tc.from, got, tc.want)
			}
		})
	}
}

func TestNodeMembership(t *testing.T) {
	g := New()
	if g.Has("a") || g.Size() != 0 {
		t.Fatal("empty graph must have no nodes")
	}
	g.AddEdge("a", "b") // both endpoints registered
	g.AddNode("lonely")
	for _, id := range []string{"a", "b", "lonely"} {
		if !g.Has(id) {
			t.Fatalf("Has(%q) = false, want true", id)
		}
	}
	if g.Has("ghost") {
		t.Fatal("Has(ghost) = true, want false")
	}
	if g.Size() != 3 {
		t.Fatalf("Size = %d, want 3", g.Size())
	}
	if g.Out("ghost") != nil || g.OutDegree("ghost") != 0 {
		t.Fatal("absent node must have nil out-edges and degree 0")
	}
}

func TestRemove(t *testing.T) {
	g := New()
	g.AddEdge("a", "b")
	g.AddEdge("c", "b")
	g.AddEdge("b", "d")
	g.Remove("b")
	if g.Has("b") {
		t.Fatal("removed node still present")
	}
	if got := g.Out("a"); len(got) != 0 {
		t.Fatalf("in-edge to removed node not cleaned: %v", got)
	}
	if got := g.Out("c"); len(got) != 0 {
		t.Fatalf("in-edge to removed node not cleaned: %v", got)
	}
	if !g.Has("d") {
		t.Fatal("unrelated node must survive removal")
	}
}
