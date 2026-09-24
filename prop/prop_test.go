package prop

import (
	"fmt"
	"maps"
	"math/rand"
	"sync"
	"testing"

	"ontology/dag"
)

var seven = []dag.Def{
	{Name: "A", Kind: "src", K: 1}, {Name: "E", Kind: "src", K: 100},
	{Name: "B", Kind: "scale", Inputs: []string{"A"}, K: 2},
	{Name: "C", Kind: "add", Inputs: []string{"A"}, K: 10},
	{Name: "D", Kind: "sum", Inputs: []string{"B", "C"}},
	{Name: "F", Kind: "sum", Inputs: []string{"D", "E"}},
	{Name: "G", Kind: "sum", Inputs: []string{"A", "D"}},
}

// naive recomputes every derived node from the sources in vals.
func naive(g *dag.Graph, vals map[string]int64) map[string]int64 {
	out := maps.Clone(vals)
	for lv := 1; lv <= g.MaxLevel; lv++ {
		for n, d := range g.Defs {
			if g.Levels[n] != lv {
				continue
			}
			out[n] = eval(d, out)
		}
	}
	return out
}

func TestCheckedBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		defs := append([]dag.Def{}, seven...)
		defs = append(defs, dag.Def{Name: "S", Kind: "src"})
		prev := "S"
		for i := 0; i < m; i++ {
			n := fmt.Sprintf("c%d", i)
			defs = append(defs, dag.Def{Name: n, Kind: "add", Inputs: []string{prev}, K: 1})
			prev = n
		}
		g, err := dag.New(defs)
		if err != nil {
			t.Fatal(err)
		}
		e := New(g)
		if _, err := e.Apply(map[string]int64{"A": 5}); err != nil {
			t.Fatal(err)
		}
		if e.checked > 40 {
			t.Errorf("m=%d: checked %d nodes, want <= 40 (independent of m)", m, e.checked)
		}
	}
}

func TestConsistency(t *testing.T) {
	for seed := int64(0); seed < 5; seed++ {
		g, _ := dag.New(seven)
		e := New(g)
		rng := rand.New(rand.NewSource(seed))
		for step := 0; step < 100; step++ {
			prev := e.View()
			sets := map[string]int64{}
			for _, s := range []string{"A", "E"} {
				if rng.Intn(2) == 0 {
					sets[s] = int64(rng.Intn(11) - 5)
				}
			}
			log, err := e.Apply(sets)
			if err != nil {
				t.Fatal(err)
			}
			cur := e.View()
			if !maps.Equal(cur, naive(g, cur)) {
				t.Fatalf("seed %d step %d: diverges from full recompute", seed, step)
			}
			pos := map[string]int{}
			for i, c := range log {
				if _, dup := pos[c.Name]; dup {
					t.Fatalf("seed %d step %d: %q logged twice", seed, step, c.Name)
				}
				pos[c.Name] = i
				if prev[c.Name] != c.Old || cur[c.Name] != c.New {
					t.Fatalf("seed %d step %d: %q old/new mismatch", seed, step, c.Name)
				}
			}
			for i, c := range log {
				for _, in := range g.Defs[c.Name].Inputs {
					if j, ok := pos[in]; ok && j > i {
						t.Fatalf("seed %d step %d: %q logged before input %q", seed, step, c.Name, in)
					}
				}
			}
		}
	}
}

func TestConcurrentView(t *testing.T) {
	e := New(mustGraph(t))
	var wg sync.WaitGroup
	bad := make(chan map[string]int64, 4)
	consistent := func(v map[string]int64) bool {
		return v["D"] == v["B"]+v["C"] && v["G"] == v["A"]+v["D"] &&
			v["F"] == v["D"]+v["E"] && v["B"] == 2*v["A"] && v["C"] == v["A"]+10
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 3000; i++ {
				if v := e.View(); !consistent(v) {
					bad <- v
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			if _, err := e.Apply(map[string]int64{"A": int64(i%9 - 4)}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
	select {
	case v := <-bad:
		t.Fatalf("reader observed a glitch: %v", v)
	default:
	}
}

func mustGraph(t *testing.T) *dag.Graph {
	t.Helper()
	g, err := dag.New(seven)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
