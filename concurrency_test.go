package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentUpsertDistinctIDs(t *testing.T) {
	s := NewStore("color")
	const workers = 16
	const perWorker = 500

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := fmt.Sprintf("w%02d-%04d", w, i)
				s.Upsert(id, map[string]any{"color": fmt.Sprintf("c%d", i%5)})
			}
		}(w)
	}
	wg.Wait()

	if n := s.RowCount(); n != workers*perWorker {
		t.Fatalf("RowCount = %d, want %d", n, workers*perWorker)
	}
	for c := 0; c < 5; c++ {
		got := s.Lookup("color", fmt.Sprintf("c%d", c))
		seen := make(map[string]bool, len(got))
		for _, id := range got {
			if seen[id] {
				t.Fatalf("duplicate id %q in lookup", id)
			}
			seen[id] = true
		}
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentReadWriteAtomicity hammers one ID with alternating
// values while readers assert they never observe a hole where the ID
// is in neither the old nor the new value's bucket.
func TestConcurrentReadWriteAtomicity(t *testing.T) {
	s := NewStore("color")
	s.Upsert("hot", map[string]any{"color": "red"})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Inspect both buckets under a single read lock so the
				// observation is one atomic snapshot of the index.
				s.mu.RLock()
				_, inRed := s.index["color"][keyOfMust("red")]["hot"]
				_, inBlue := s.index["color"][keyOfMust("blue")]["hot"]
				s.mu.RUnlock()
				if inRed == inBlue {
					t.Errorf("half-updated state: red=%v blue=%v", inRed, inBlue)
					return
				}
			}
		}()
	}
	for i := 0; i < 2000; i++ {
		v := "red"
		if i%2 == 1 {
			v = "blue"
		}
		s.Upsert("hot", map[string]any{"color": v})
	}
	close(stop)
	wg.Wait()
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
}
