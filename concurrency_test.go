package ontology

import (
	"sync"
	"testing"
)

// TestConcurrentSetClear: many goroutines set and clear disjoint bit ranges
// while readers call Bytes and Count; afterwards the encoding must be
// canonical and the count exact. Run with -race.
func TestConcurrentSetClear(t *testing.T) {
	b := New()
	const workers = 8
	const perWorker = 2000
	// Pre-set a region that workers will concurrently clear.
	b.SetRange(0, workers*perWorker-1)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint32(w * perWorker)
			for i := uint32(0); i < perWorker; i++ {
				b.Clear(base + i) // disjoint ranges: no lost updates
			}
		}(w)
	}
	// Concurrent readers must never observe a half-updated encoding.
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = b.Bytes()
				_ = b.Count()
				if err := b.Verify(); err != nil {
					t.Errorf("reader observed non-canonical state: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	readers.Wait()
	if err := b.Verify(); err != nil {
		t.Fatalf("Verify after concurrent clears: %v", err)
	}
	if got := b.Count(); got != 0 {
		t.Fatalf("Count = %d, want 0", got)
	}
}

// TestConcurrentSetDisjoint: goroutines set disjoint ranges; no update
// may be lost and the final encoding must be canonical.
func TestConcurrentSetDisjoint(t *testing.T) {
	b := New()
	const workers = 8
	const perWorker = 500
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint32(w * perWorker * 2) // gaps keep ranges disjoint
			for i := uint32(0); i < perWorker; i++ {
				b.Set(base + i)
			}
		}(w)
	}
	wg.Wait()
	if err := b.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got, want := b.Count(), uint64(workers*perWorker); got != want {
		t.Fatalf("Count = %d, want %d", got, want)
	}
}
