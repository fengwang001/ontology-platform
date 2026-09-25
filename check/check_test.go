package check

import "errors"
import "slices"
import "sync"
import "testing"
import "ontology/topo"

type testCase struct {
	name    string
	n       int
	edges   []topo.Edge
	want    []int
	wantErr error
	not     error
	on      []int
}

var cases = []testCase{
	{"empty", 0, nil, []int{}, nil, nil, nil},
	{"chain", 4, []topo.Edge{{U: 0, V: 1}, {U: 1, V: 2}, {U: 2, V: 3}}, []int{0, 1, 2, 3}, nil, nil, nil},
	{"diamond", 4, []topo.Edge{{U: 0, V: 1}, {U: 0, V: 2}, {U: 1, V: 3}, {U: 2, V: 3}}, nil, nil, nil, nil},
	{"wide", 6, []topo.Edge{{U: 0, V: 3}, {U: 1, V: 3}, {U: 2, V: 4}, {U: 3, V: 5}, {U: 4, V: 5}}, nil, nil, nil, nil},
	{"self-loop", 1, []topo.Edge{{U: 0, V: 0}}, nil, topo.ErrCycle, topo.ErrBadEdge, []int{0}},
	{"two-cycle", 2, []topo.Edge{{U: 0, V: 1}, {U: 1, V: 0}}, nil, topo.ErrCycle, topo.ErrBadEdge, []int{0, 1}},
	{"triangle", 4, []topo.Edge{{U: 0, V: 1}, {U: 1, V: 2}, {U: 2, V: 0}, {U: 2, V: 3}}, nil, topo.ErrCycle, topo.ErrBadEdge, []int{0, 1, 2}},
	{"neg-src", 2, []topo.Edge{{U: -1, V: 0}}, nil, topo.ErrBadEdge, topo.ErrCycle, nil},
	{"dst-oob", 2, []topo.Edge{{U: 0, V: 2}}, nil, topo.ErrBadEdge, topo.ErrCycle, nil},
}

func TestTopoSort(t *testing.T) { // 拓扑序正确 + Kahn 参照一致 + 边界 + 两类哨兵错误
	for _, c := range cases {
		got, err := topo.TopoSort(c.n, c.edges)
		if c.wantErr != nil {
			if !errors.Is(err, c.wantErr) || errors.Is(err, c.not) {
				t.Errorf("%s: err=%v want %v", c.name, err, c.wantErr)
			}
			var ce *topo.CycleError
			if c.on != nil && (!errors.As(err, &ce) || !slices.Contains(c.on, ce.Node)) {
				t.Errorf("%s: bad cycle node: %v", c.name, err)
			}
			continue
		}
		ref, rerr := Kahn(c.n, c.edges)
		if err != nil || rerr != nil || !validOrder(c.n, c.edges, got) || !validOrder(c.n, c.edges, ref) {
			t.Errorf("%s: got=%v(%v) ref=%v(%v)", c.name, got, err, ref, rerr)
		}
		if c.want != nil && !slices.Equal(got, c.want) {
			t.Errorf("%s: got=%v want=%v", c.name, got, c.want)
		}
	}
}

func TestDiamondNoFalseCycle(t *testing.T) { // 钻石图：三色接受，两色误报
	c := cases[2]
	if _, err := topo.TopoSort(c.n, c.edges); err != nil {
		t.Fatalf("diamond rejected: %v", err)
	}
	seen := make([]bool, c.n)
	var dfs func(u int) bool
	dfs = func(u int) bool { // 两色错误判据：已访问即判环
		if seen[u] {
			return true
		}
		seen[u] = true
		for _, e := range c.edges {
			if e.U == u && dfs(e.V) {
				return true
			}
		}
		return false
	}
	if !dfs(0) {
		t.Fatal("two-color impl should misjudge diamond as cyclic")
	}
}

func TestEdgeVisitsAndConcurrency(t *testing.T) { // 边恰访问一次；-race 纯净且结果确定
	const n, m = 1000, 5000
	big := make([]topo.Edge, m)
	for i := range big {
		big[i] = topo.Edge{U: i % 999, V: i%999 + 1 + (i*7)%(999-i%999)}
	}
	if v := topo.EdgeVisits(n, big); v > m {
		t.Fatalf("visits=%d > m=%d", v, m)
	}
	want, _ := topo.TopoSort(cases[3].n, cases[3].edges)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := topo.TopoSort(cases[3].n, cases[3].edges)
			if err != nil || !slices.Equal(got, want) {
				t.Errorf("got=%v err=%v", got, err)
			}
		}()
	}
	wg.Wait()
}
