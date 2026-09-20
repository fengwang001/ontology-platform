package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentUpsertDistinctIDs(t *testing.T) {
	s := NewStore("color")
	const workers = 16
	const perWorker = 250
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
	if got := s.TotalRows(); got != workers*perWorker {
		t.Fatalf("TotalRows = %d, want %d", got, workers*perWorker)
	}
	if got := s.TotalEntries("color"); got != workers*perWorker {
		t.Fatalf("TotalEntries = %d, want %d", got, workers*perWorker)
	}
	seen := make(map[string]bool)
	for c := 0; c < 5; c++ {
		for _, id := range s.Lookup("color", fmt.Sprintf("c%d", c)) {
			if seen[id] {
				t.Fatalf("id %q appears under two values", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("indexed ids = %d, want %d", len(seen), workers*perWorker)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestConcurrentReadersSeeNoHalfUpdate(t *testing.T) {
	s := NewStore("color")
	const flippers = 8
	ids := make([]string, flippers)
	for f := 0; f < flippers; f++ {
		ids[f] = fmt.Sprintf("flip-%d", f)
		s.Upsert(ids[f], map[string]any{"color": "red"})
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			redKey, _ := keyOf("red")
			blueKey, _ := keyOf("blue")
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Take one read lock so both buckets are observed in a
				// single consistent snapshot.
				s.mu.RLock()
				ix := s.indexes["color"]
				for _, id := range ids {
					_, inRed := ix.buckets[redKey][id]
					_, inBlue := ix.buckets[blueKey][id]
					if inRed == inBlue {
						s.mu.RUnlock()
						t.Errorf("id %q in red=%v blue=%v: half-updated state",
							id, inRed, inBlue)
						return
					}
				}
				s.mu.RUnlock()
			}
		}()
	}
	var flippersWG sync.WaitGroup
	for f := 0; f < flippers; f++ {
		flippersWG.Add(1)
		go func(id string) {
			defer flippersWG.Done()
			for i := 0; i < 500; i++ {
				v := "red"
				if i%2 == 1 {
					v = "blue"
				}
				s.Upsert(id, map[string]any{"color": v})
			}
		}(ids[f])
	}
	flippersWG.Wait()
	for i := 0; i < 200; i++ {
		if err := s.Verify(); err != nil {
			t.Fatalf("Verify during churn: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestConcurrentUpsertSameID(t *testing.T) {
	s := NewStore("color")
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				s.Upsert("shared", map[string]any{"color": fmt.Sprintf("c%d", g%3)})
			}
		}(g)
	}
	wg.Wait()
	if got := s.TotalRows(); got != 1 {
		t.Fatalf("TotalRows = %d, want 1", got)
	}
	if got := s.TotalEntries("color"); got != 1 {
		t.Fatalf("TotalEntries = %d, want 1", got)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
