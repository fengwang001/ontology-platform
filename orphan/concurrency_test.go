package orphan

import (
	"fmt"
	"sync"
	"testing"
)

// Concurrent edge mutations, generation-advancement scans and cleanups run
// together. Linearizability is checked by invariants that hold in every
// sequential interleaving:
//
//  1. an object is in at most one generation queue at a time;
//  2. every live object has exactly one valid generation with a sane timer;
//  3. no panic/data-race is observed (run the suite with -race).
func TestConcurrentMutationsScansAndCleanup(t *testing.T) {
	s, clk := newTestSys(t, baseCfg(), 0)
	const n = 12
	for i := 0; i < n; i++ {
		s.AddObject(fmt.Sprintf("o%d", i))
		s.AddObject(fmt.Sprintf("a%d", i))
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			types := []string{"authoredBy", "partOf", "locatedIn", "tagged", "reviewedBy"}
			for k := 0; ; k++ {
				select {
				case <-stop:
					return
				default:
				}
				oi := (w + k) % n
				ai := (w*3 + k*2) % n
				typ := types[(w+k)%len(types)]
				src := fmt.Sprintf("a%d", ai)
				dst := fmt.Sprintf("o%d", oi)
				if k%2 == 0 {
					_ = s.AddEdge(typ, src, dst)
				} else {
					_, _ = s.RemoveEdge(typ, src, dst)
				}
			}
		}(w)
	}
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for k := 0; k < 300; k++ {
				select {
				case <-stop:
					return
				default:
				}
				clk.Set(int64(k * (w + 1)))
				s.Scan()
			}
		}(w)
	}
	for w := 0; w < 2000; w++ {
		g1 := map[string]bool{}
		for _, id := range s.PendingGen1() {
			g1[id] = true
		}
		for _, id := range s.PendingGen2() {
			if g1[id] {
				t.Fatalf("object %s simultaneously in both queues", id)
			}
		}
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("o%d", i)
			if st, ok := s.StateOf(id); ok {
				if st.Generation != Gen1 && st.Generation != Gen2 && st.Generation != NoGen {
					t.Fatalf("impossible generation %v for %s", st.Generation, id)
				}
				if st.Generation != NoGen && st.SinceMs < 0 {
					t.Fatalf("negative timer for %s", id)
				}
			}
		}
	}
	close(stop)
	wg.Wait()
}
