package alloc

import (
	"fmt"
	"sync"
	"testing"

	"ontology/node"
)

// buildStableCluster assigns every copy of the given total-copy count so that
// afterwards nothing is unassigned, no node is excluded and no node is over
// the high watermark. Returns the cluster after its settling reroutes.
func buildStableCluster(t *testing.T, totalCopies int) (*node.Cluster, uint64) {
	t.Helper()
	// 8 nodes in 8 distinct zones removes D3 pressure; generous disks remove D4.
	cl, err := node.NewCluster(80, 90)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if err := cl.AddNode(fmt.Sprintf("n%d", i), fmt.Sprintf("z%d", i), 1e12); err != nil {
			t.Fatal(err)
		}
	}
	// Shards of size 1, one index holds up to 64 shards, r=0 -> one copy each.
	remaining := totalCopies
	idxSeq := 0
	for remaining > 0 {
		s := remaining
		if s > 64 {
			s = 64
		}
		name := fmt.Sprintf("i%03d", idxSeq)
		idxSeq++
		if err := cl.CreateIndex(name, s, 0, 1); err != nil {
			t.Fatal(err)
		}
		remaining -= s
	}
	var evals uint64
	cl.Lock()
	r := rerouteLocked(cl, &evals)
	cl.Unlock()
	if len(r.Moved) != 0 || len(r.Assigned) != totalCopies {
		t.Fatalf("settling reroute assigned=%d moved=%d total=%d", len(r.Assigned), len(r.Moved), totalCopies)
	}
	return cl, evals
}

func TestEvalsZeroWhenStable(t *testing.T) {
	for _, total := range []int{100, 10000} {
		t.Run(fmt.Sprintf("%d copies", total), func(t *testing.T) {
			cl, settlingEvals := buildStableCluster(t, total)
			if settlingEvals == 0 {
				t.Fatal("initial placement must evaluate rules")
			}
			var evals uint64
			cl.Lock()
			r := rerouteLocked(cl, &evals)
			cl.Unlock()
			if len(r.Assigned) != 0 || len(r.Moved) != 0 {
				t.Fatalf("stable cluster must be a no-op: A=%d M=%d", len(r.Assigned), len(r.Moved))
			}
			if evals != 0 {
				t.Fatalf("stable cluster with %d copies performed %d rule evaluations, want 0", total, evals)
			}
			t.Logf("copies=%d: second Reroute evals=0 (settling evals=%d)", total, settlingEvals)
		})
	}
}

func TestConcurrentReroutesAreSafeAndStable(t *testing.T) {
	cl, _ := buildStableCluster(t, 200)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				r := Reroute(cl)
				if len(r.Assigned) != 0 || len(r.Moved) != 0 {
					t.Errorf("concurrent stable reroute produced work: %+v", r)
					return
				}
				if _, err := Explain(cl, "i000", 0, true); err != nil {
					t.Errorf("concurrent explain: %v", err)
					return
				}
			}
		}()
	}
	// Concurrent mutators on disjoint ids plus readers.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := fmt.Sprintf("cx%d", g)
			_ = cl.AddNode(id, fmt.Sprintf("zx%d", g), 1e12)
			for k := 0; k < 50; k++ {
				_ = cl.SetOther(id, int64(k))
				_ = cl.SetExclude(id, k%2 == 0)
				_ = Reroute(cl)
			}
		}(g)
	}
	wg.Wait()

	// After all churn settles (exclude flags cleared), final state must still
	// satisfy the per-shard uniqueness invariant.
	for g := 0; g < 4; g++ {
		if err := cl.SetExclude(fmt.Sprintf("cx%d", g), false); err != nil {
			t.Fatal(err)
		}
	}
	_ = Reroute(cl)
	assertInvariants(t, cl, -1, -1)
}
