package orphan

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// Randomized differential test: a long stream of identical operations is
// replayed against System and the independently written NaiveModel, and after
// EVERY step the full observable state (existence, generation, timer, both
// queue contents) is compared entry by entry.
func TestRandomDifferentialVsNaive(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			clk := &ManualClock{}
			cfg := Config{
				Types: map[string]TypeConfig{
					"ind": {Kind: Independent},
					"p":   {Kind: Joint, Requires: []string{"q"}},
					"q":   {Kind: Joint, Requires: []string{"p"}},
					"x":   {Kind: Joint, Requires: []string{"y"}},
					"y":   {Kind: Joint, Requires: []string{"x"}},
				},
				GraceGen1Ms: 1 + rng.Int63n(8),
				GraceGen2Ms: 1 + rng.Int63n(5),
			}
			s, err := New(cfg, clk, nil)
			if err != nil {
				t.Fatal(err)
			}
			nv, err := NewNaive(cfg, clk)
			if err != nil {
				t.Fatal(err)
			}
			types := []string{"ind", "p", "q", "x", "y"}
			objects := []string{"o0", "o1", "o2", "o3", "o4", "o5", "o6", "o7"}
			var live []string

			var present []opEdge
			step := 0
			compare := func() {
				t.Helper()
				var liveReal, liveNaive []string
				realState := map[string]State{}
				naiveState := map[string]State{}
				for _, id := range objects {
					if st, ok := s.StateOf(id); ok {
						liveReal = append(liveReal, id)
						realState[id] = st
					}
					if st, ok := nv.StateOf(id); ok {
						liveNaive = append(liveNaive, id)
						naiveState[id] = st
					}
				}
				sort.Strings(liveReal)
				sort.Strings(liveNaive)
				if fmt.Sprint(liveReal) != fmt.Sprint(liveNaive) {
					t.Fatalf("step %d seed %d: live sets differ real=%v naive=%v", step, seed, liveReal, liveNaive)
				}
				for _, id := range liveReal {
					a, b := realState[id], naiveState[id]
					if a.Generation != b.Generation || a.SinceMs != b.SinceMs {
						t.Fatalf("step %d seed %d: state of %s differs real=%+v naive=%+v", step, seed, id, a, b)
					}
				}
				if fmt.Sprint(s.PendingGen1()) != fmt.Sprint(nv.PendingGen1()) {
					t.Fatalf("step %d seed %d: gen1 queues differ %v vs %v", step, seed, s.PendingGen1(), nv.PendingGen1())
				}
				if fmt.Sprint(s.PendingGen2()) != fmt.Sprint(nv.PendingGen2()) {
					t.Fatalf("step %d seed %d: gen2 queues differ %v vs %v", step, seed, s.PendingGen2(), nv.PendingGen2())
				}
			}
			for step = 0; step < 1500; step++ {
				switch rng.Intn(10) {
				case 0, 1: // add object
					for _, id := range objects {
						s.AddObject(id)
						nv.AddObject(id)
						if !contains(live, id) {
							live = append(live, id)
						}
					}
				case 2, 3, 4: // add edge
					if len(live) >= 2 {
						typ := types[rng.Intn(len(types))]
						src := live[rng.Intn(len(live))]
						dst := live[rng.Intn(len(live))]
						e1 := s.AddEdge(typ, src, dst)
						e2 := nv.AddEdge(typ, src, dst)
						sameErr(e1, e2, t, step)
						if e1 == nil && !containsEdge(present, typ, src, dst) {
							present = append(present, opEdge{typ, src, dst})
						}
					}
				case 5, 6: // remove edge
					if len(present) > 0 {
						i := rng.Intn(len(present))
						r := present[i]
						ok1, e1 := s.RemoveEdge(r.typ, r.src, r.dst)
						ok2, e2 := nv.RemoveEdge(r.typ, r.src, r.dst)
						sameErr(e1, e2, t, step)
						if ok1 != ok2 {
							t.Fatalf("step %d: remove presence mismatch %v vs %v", step, ok1, ok2)
						}
						if ok1 {
							present = append(present[:i], present[i+1:]...)
						}
					}
				case 7: // remove edge that may not exist
					if len(live) >= 2 {
						typ := types[rng.Intn(len(types))]
						src := live[rng.Intn(len(live))]
						dst := live[rng.Intn(len(live))]
						_, e1 := s.RemoveEdge(typ, src, dst)
						_, e2 := nv.RemoveEdge(typ, src, dst)
						sameErr(e1, e2, t, step)
						// keep present list authoritative by rebuilding it
						present = present[:0]
						for _, e := range nv.Edges() {
							present = append(present, opEdge{e.Type, e.Src, e.Dst})
						}
					}
				default: // advance clock + scan
					clk.Advance(rng.Int63n(6))
					s.Scan()
					nv.Scan()
					live = live[:0]
					present = present[:0]
					for _, id := range objects {
						if _, ok := nv.StateOf(id); ok {
							live = append(live, id)
						}
					}
					for _, e := range nv.Edges() {
						present = append(present, opEdge{e.Type, e.Src, e.Dst})
					}
				}
				compare()
			}
		})
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

type opEdge struct{ typ, src, dst string }

func containsEdge(xs []opEdge, typ, src, dst string) bool {
	for _, r := range xs {
		if r.typ == typ && r.src == src && r.dst == dst {
			return true
		}
	}
	return false
}

func sameErr(e1, e2 error, t *testing.T, step int) {
	t.Helper()
	if errors.Is(e1, ErrObjectNotFound) != errors.Is(e2, ErrObjectNotFound) ||
		errors.Is(e1, ErrTypeNotConfigured) != errors.Is(e2, ErrTypeNotConfigured) ||
		(e1 == nil) != (e2 == nil) {
		t.Fatalf("step %d: error mismatch real=%v naive=%v", step, e1, e2)
	}
}
