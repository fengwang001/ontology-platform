package check

import (
	"errors"
	"fmt"
	"ontology/topo"
	"sync"
	"testing"
)

func twoColorCycle(n int, edges [][2]int) bool { // buggy "visited == cycle", see NOTES.md
	seen, cyc := make([]bool, n), false
	var dfs func(u int)
	dfs = func(u int) {
		if seen[u] {
			cyc = true
			return
		}
		seen[u] = true
		for _, e := range edges {
			if e[0] == u {
				dfs(e[1])
			}
		}
	}
	for u := 0; u < n; u++ {
		dfs(u)
	}
	return cyc
}

func TestTopoSort(t *testing.T) {
	cases := []struct {
		name    string
		n       int
		edges   [][2]int
		want    []int
		wantErr error
		buggy   bool
	}{
		{"empty", 0, nil, []int{}, nil, false},
		{"chain", 5, [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}}, []int{0, 1, 2, 3, 4}, nil, true},
		{"diamond", 4, [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}}, []int{0, 2, 1, 3}, nil, true},
		{"self loop", 1, [][2]int{{0, 0}}, nil, topo.ErrCycle, false},
		{"3-cycle", 3, [][2]int{{0, 1}, {1, 2}, {2, 0}}, nil, topo.ErrCycle, false},
		{"u out of range", 2, [][2]int{{0, 2}}, nil, topo.ErrBadEdge, false},
		{"negative u", 2, [][2]int{{-1, 0}}, nil, topo.ErrBadEdge, false},
	}
	for _, c := range cases {
		order, err := topo.TopoSort(c.n, c.edges)
		if !errors.Is(err, c.wantErr) || (c.wantErr == topo.ErrCycle && err.Error() != "topo: cycle detected: node 0") {
			t.Fatalf("%s: err=%q, want %v", c.name, err, c.wantErr)
		}
		if c.wantErr != nil {
			continue
		}
		if _, acyclic := Kahn(c.n, c.edges); !acyclic {
			t.Fatalf("%s: Kahn disagrees", c.name)
		}
		pos := make([]int, c.n)
		for i, v := range order {
			pos[v] = i
		}
		ok := len(order) == c.n
		for _, e := range c.edges {
			ok = ok && pos[e[0]] < pos[e[1]]
		}
		again, _ := topo.TopoSort(c.n, c.edges)
		if !ok || fmt.Sprint(order) != fmt.Sprint(c.want) || fmt.Sprint(again) != fmt.Sprint(order) {
			t.Fatalf("%s: order=%v again=%v", c.name, order, again)
		}
		if got := twoColorCycle(c.n, c.edges); got != c.buggy {
			t.Fatalf("%s: two-color cycle=%v, want %v", c.name, got, c.buggy)
		}
	}
}

func TestScaleAndRace(t *testing.T) {
	edges := make([][2]int, 5000)
	for i := range edges {
		edges[i] = [2]int{i % 999, i%999 + 1}
	}
	topo.ResetEdgeVisits()
	want, err := topo.TopoSort(1000, edges)
	if err != nil || topo.EdgeVisits() > 5000 {
		t.Fatalf("err=%v visits=%d", err, topo.EdgeVisits())
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := topo.TopoSort(1000, edges); err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("got=%v err=%v", got, err)
			}
		}()
	}
	wg.Wait()
}
