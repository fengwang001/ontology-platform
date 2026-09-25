package traverse

import (
	"errors"
	"reflect"
	"testing"

	"ontology/graph"
)

func build(t *testing.T, nodes []string, edges [][2]string) *graph.Graph {
	t.Helper()
	g := graph.New()
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

func TestWalk(t *testing.T) {
	// 菱形 + 环尾：a→b, a→c, b→d, c→d, d→a（环），d→e
	nodes := []string{"a", "b", "c", "d", "e"}
	edges := [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}, {"d", "a"}, {"d", "e"}}

	cases := []struct {
		name  string
		start string
		dir   Dir
		want  []string
	}{
		{"out dedups diamond and cycle", "a", Out, []string{"a", "b", "c", "d", "e"}},
		{"in walks upstream", "d", In, []string{"d", "b", "c", "a"}},
		{"both covers both sides", "b", Both, []string{"b", "a", "d", "c", "e"}},
		{"cycle terminates", "d", Out, []string{"d", "a", "e", "b", "c"}},
	}
	for _, tc := range cases {
		g := build(t, nodes, edges)
		got, err := Walk(g, tc.start, tc.dir)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWalkErrors(t *testing.T) {
	g := build(t, []string{"a"}, nil)
	cases := []struct {
		name  string
		start string
		dir   Dir
		want  error
	}{
		{"start missing", "x", Out, graph.ErrNodeNotFound},
		{"invalid dir", "a", Dir(99), ErrInvalidDir},
	}
	for _, tc := range cases {
		if _, err := Walk(g, tc.start, tc.dir); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}
