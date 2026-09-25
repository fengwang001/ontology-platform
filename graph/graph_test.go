package graph

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"ontology/fail"
)

func build(nodes []string, edges [][2]string) *Graph {
	g := New()
	for _, n := range nodes {
		g.AddNode(ID(n))
	}
	for _, e := range edges {
		if err := g.AddEdge(ID(e[0]), ID(e[1])); err != nil {
			panic(err)
		}
	}
	return g
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name  string
		nodes []string
		edges [][2]string
		cycle bool
	}{
		{"empty", nil, nil, false},
		{"dag", []string{"a", "b", "c"}, [][2]string{{"a", "b"}, {"b", "c"}}, false},
		{"self-loop", []string{"a"}, [][2]string{{"a", "a"}}, true},
		{"two-cycle", []string{"a", "b"}, [][2]string{{"a", "b"}, {"b", "a"}}, true},
		{"triangle", []string{"a", "b", "c"}, [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}}, true},
	}
	for _, c := range cases {
		err := build(c.nodes, c.edges).Check()
		if !c.cycle {
			if err != nil {
				t.Errorf("%s: unexpected %v", c.name, err)
			}
			continue
		}
		var ce *CycleError
		if !errors.As(err, &ce) || !errors.Is(err, fail.ErrCycle) {
			t.Errorf("%s: want CycleError, got %v", c.name, err)
			continue
		}
		p := ce.Path
		if len(p) < 2 || p[0] != p[len(p)-1] {
			t.Errorf("%s: path not closed: %v", c.name, p)
			continue
		}
		set := map[[2]ID]bool{}
		for _, e := range c.edges {
			set[[2]ID{ID(e[0]), ID(e[1])}] = true
		}
		for i := 0; i+1 < len(p); i++ {
			if !set[[2]ID{p[i], p[i+1]}] {
				t.Errorf("%s: edge %s->%s not in input", c.name, p[i], p[i+1])
			}
		}
	}
}

func TestAddEdge(t *testing.T) {
	cases := []struct {
		name     string
		from, to ID
		want     error
	}{
		{"ok", "a", "b", nil},
		{"dup-idempotent", "a", "b", nil},
		{"unknown-from", "ghost", "b", fail.ErrMissingDep},
		{"unknown-to", "a", "ghost", fail.ErrMissingDep},
	}
	g := New()
	g.AddNode("a")
	g.AddNode("b")
	for _, c := range cases {
		if err := g.AddEdge(c.from, c.to); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if n := len(g.Succ("a")); n != 1 {
		t.Errorf("dup edge not idempotent: succ(a)=%d, want 1", n)
	}
}

func TestLayers(t *testing.T) {
	chainNodes := make([]string, 1000)
	chainEdges := make([][2]string, 0, 999)
	for i := range chainNodes {
		chainNodes[i] = fmt.Sprintf("n%04d", i)
		if i > 0 {
			chainEdges = append(chainEdges, [2]string{chainNodes[i-1], chainNodes[i]})
		}
	}
	cases := []struct {
		name  string
		nodes []string
		edges [][2]string
		want  [][]ID
	}{
		{"empty", nil, nil, nil},
		{"single", []string{"a"}, nil, [][]ID{{"a"}}},
		{"independent", []string{"b", "a"}, nil, [][]ID{{"a", "b"}}},
		{"chain", []string{"a", "b", "c"}, [][2]string{{"a", "b"}, {"b", "c"}}, [][]ID{{"a"}, {"b"}, {"c"}}},
		{"diamond", []string{"a", "b", "c", "d"}, [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}}, [][]ID{{"a"}, {"b", "c"}, {"d"}}},
	}
	for _, c := range cases {
		got, err := build(c.nodes, c.edges).Layers()
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	layers, err := build(chainNodes, chainEdges).Layers() // 1000 层链：不得栈溢出
	if err != nil || len(layers) != 1000 {
		t.Errorf("chain-1000: layers=%d err=%v", len(layers), err)
	}
}
