package engine_test

import (
	"maps"
	"math/rand"
	"testing"

	"ontology/engine"
	"ontology/model"
)

// pick returns k distinct integers from [lo, hi].
func pick(rng *rand.Rand, lo, hi, k int) []int {
	perm := rng.Perm(hi - lo + 1)
	out := make([]int, k)
	for i := range out {
		out[i] = lo + perm[i]
	}
	return out
}

// genGraph builds a random valid DAG with at least one task, one choice
// split and one join. Edges only point to higher ids, so it is acyclic.
func genGraph(rng *rand.Rand) *model.Graph {
	for try := 0; try < 20000; try++ {
		n := 4 + rng.Intn(5)
		kind := make([]model.NodeType, n+1)
		kind[1], kind[n] = model.Start, model.End
		ok := true
		for v := 2; v < n; v++ {
			kind[v] = model.NodeType(1 + rng.Intn(7))
			if (kind[v] == model.AndSplit || model.IsChoiceSplit(kind[v])) && n-v < 2 {
				ok = false
			}
		}
		if !ok {
			continue
		}
		var edges []model.Edge
		for u := 1; u < n; u++ {
			d := 1
			if kind[u] == model.AndSplit || model.IsChoiceSplit(kind[u]) {
				hi := n - u
				if hi > 3 {
					hi = 3
				}
				d = 2 + rng.Intn(hi-1)
			}
			for _, w := range pick(rng, u+1, n, d) {
				edges = append(edges, model.Edge{From: u, To: w})
			}
		}
		g := &model.Graph{N: n, Kind: kind, Edges: edges}
		if model.Validate(g) != nil {
			continue
		}
		tasks, splits, joins := 0, 0, 0
		for v := 1; v <= n; v++ {
			switch {
			case kind[v] == model.Task:
				tasks++
			case model.IsChoiceSplit(kind[v]):
				splits++
			case model.IsJoin(kind[v]):
				joins++
			}
		}
		if tasks >= 1 && tasks <= 8 && splits >= 1 && joins >= 1 {
			return g
		}
	}
	return nil
}

func genChoices(rng *rand.Rand, g *model.Graph) map[int][]int {
	m := map[int][]int{}
	for v := 1; v <= g.N; v++ {
		out := len(g.Out(v))
		switch g.Kind[v] {
		case model.XorSplit:
			m[v] = []int{rng.Intn(out)}
		case model.OrSplit:
			m[v] = pick(rng, 0, out-1, 1+rng.Intn(out))
		}
	}
	return m
}

// explore walks every interleaving of task completions and returns the
// set of terminal states (no active task left).
func explore(s0 *naive) (map[string]*naive, bool) {
	seen := map[string]bool{}
	terms := map[string]*naive{}
	var walk func(s *naive) bool
	walk = func(s *naive) bool {
		k := s.key()
		if seen[k] {
			return true
		}
		seen[k] = true
		if len(seen) > 50000 {
			return false
		}
		if len(s.act) == 0 {
			terms[k] = s
			return true
		}
		for t := range s.act {
			c := s.clone()
			c.complete(t)
			if !walk(c) {
				return false
			}
		}
		return true
	}
	if !walk(s0) {
		return nil, false
	}
	return terms, true
}

func countTasks(g *model.Graph) int {
	c := 0
	for v := 1; v <= g.N; v++ {
		if g.Kind[v] == model.Task {
			c++
		}
	}
	return c
}

func firesEq(a, b map[int]int) bool {
	return maps.Equal(a, b)
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	completed, stuck, explored := 0, 0, 0
	for trial := 0; trial < 1000; trial++ {
		g := genGraph(rng)
		if g == nil {
			t.Fatal("graph generator exhausted")
		}
		choices := genChoices(rng, g)
		in := engine.NewInstance(g, choices)
		cur := newNaive(g, choices)
		var order []int
		for len(cur.act) > 0 {
			keys := make([]int, 0, len(cur.act))
			for k := range cur.act {
				keys = append(keys, k)
			}
			task := keys[rng.Intn(len(keys))]
			order = append(order, task)
			cur.complete(task)
			if err := in.Complete(task); err != nil {
				t.Fatalf("trial %d: Complete(%d): %v\ngraph=%v\nchoices=%v", trial, task, err, g.Edges, choices)
			}
			if s := in.Status(); s.EndCount != cur.end || !firesEq(s.Fires, cur.fires) {
				t.Fatalf("trial %d after %v: engine=%+v naive: %s\ngraph=%v choices=%v",
					trial, order, s, cur.key(), g.Edges, choices)
			}
		}
		final := in.Status()
		wantState := engine.Completed
		if len(cur.arr) > 0 {
			wantState = engine.Stuck
		}
		if final.State != wantState {
			t.Fatalf("trial %d: state %v, want %v (%s)", trial, final.State, wantState, cur.key())
		}
		if wantState == engine.Stuck {
			stuck++
		} else {
			completed++
		}
		if countTasks(g) <= 5 {
			if terms, ok := explore(newNaive(g, choices)); ok {
				explored++
				if len(terms) != 1 || terms[cur.key()] == nil {
					t.Fatalf("trial %d: order-dependent terminals: %d, want {%s}", trial, len(terms), cur.key())
				}
			}
		}
		replay := engine.NewInstance(g, choices)
		for _, task := range order {
			if err := replay.Complete(task); err != nil {
				t.Fatalf("trial %d replay: %v", trial, err)
			}
		}
		if s := replay.Status(); s.State != final.State || s.EndCount != final.EndCount || !firesEq(s.Fires, final.Fires) {
			t.Fatalf("trial %d: replay diverged: %+v vs %+v", trial, s, final)
		}
		t.Logf("trial %d: n=%d edges=%v choices=%v order=%v -> state=%v end=%d fires=%v",
			trial, g.N, g.Edges, choices, order, final.State, final.EndCount, final.Fires)
	}
	t.Logf("summary: completed=%d stuck=%d fully-explored=%d (all terminals unique)", completed, stuck, explored)
}
