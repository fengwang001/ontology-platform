package api

import (
	"sync"
	"testing"
)

// naiveReachable returns objects reachable along child edges from the given
// roots, using only the exported snapshot view.
func naiveReachable(c *Collector, roots []Obj) map[Obj]bool {
	snaps := c.Snapshot()
	child := make(map[Obj]Obj, len(snaps))
	alive := make(map[Obj]bool, len(snaps))
	for _, s := range snaps {
		child[s.O], alive[s.O] = s.Child, true
	}
	reach := map[Obj]bool{}
	var st []Obj
	for _, r := range roots {
		if alive[r] && !reach[r] {
			reach[r], st = true, append(st, r)
		}
	}
	for len(st) > 0 {
		x := st[len(st)-1]
		st = st[:len(st)-1]
		if d := child[x]; d != 0 && !reach[d] {
			reach[d], st = true, append(st, d)
		}
	}
	return reach
}

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrent is the section-six race test: N goroutines each Root their
// own object, then Point at one another concurrently; after joining, every
// reachability, conservation and dangling invariant must hold. No sleeps.
func TestConcurrent(t *testing.T) {
	for _, N := range []int{8, 32, 64} {
		c := New()
		objs := make([]Obj, N)
		rooted := make([]bool, N)
		var rootedMu sync.Mutex
		var rootsDone, pointsDone, all sync.WaitGroup
		rootsDone.Add(N)
		pointsDone.Add(N)
		all.Add(N)
		for i := 0; i < N; i++ {
			go func(i int) {
				defer all.Done()
				o, err := c.Root()
				if err != nil {
					t.Errorf("Root: %v", err)
					return
				}
				objs[i] = o // each goroutine writes only its own slot
				rootsDone.Done()
				rootsDone.Wait()
				seed := uint64(i*2654435761 + 1)
				for k := 0; k < 6; k++ { // mutually Point at peers
					seed = seed*6364136223846793005 + 1442695040888963407
					j := int(seed % uint64(N))
					if err := c.Point(o, objs[j]); err != nil {
						t.Errorf("Point: %v", err)
						return
					}
				}
				pointsDone.Done()
				pointsDone.Wait()
				if i%3 == 0 { // drop some roots to leave rootless garbage
					if err := c.Unroot(o); err != nil {
						t.Errorf("Unroot: %v", err)
						return
					}
					rootedMu.Lock()
					rooted[i] = false
					rootedMu.Unlock()
				} else {
					rootedMu.Lock()
					rooted[i] = true
					rootedMu.Unlock()
				}
			}(i)
		}
		all.Wait()
		if c.Collect() < 0 {
			t.Fatal("Collect returned negative count")
		}
		var roots []Obj
		for i := range rooted {
			if rooted[i] {
				roots = append(roots, objs[i])
			}
		}
		want := naiveReachable(c, roots)
		snaps := c.Snapshot()
		indeg := map[Obj]int{}
		for _, s := range snaps {
			if s.Child != 0 {
				if !want[s.Child] { // child is either dangling or unreachable
					t.Fatalf("dangling or unreachable child %d", s.Child)
				}
				indeg[s.Child]++
			}
		}
		for _, s := range snaps {
			if !want[s.O] {
				t.Fatalf("object %d survived Collect but is root-unreachable", s.O)
			}
			if s.Fields != indeg[s.O] { // rc conservation: real field indegree
				t.Fatalf("object %d fields=%d indegree=%d", s.O, s.Fields, indeg[s.O])
			}
		}
	}
}
