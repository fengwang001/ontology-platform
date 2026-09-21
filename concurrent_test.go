package ontology

import (
	"sync"
	"testing"
)

// TestConcurrentSetClear hammers one set from many goroutines: writers
// Set/Clear disjoint bit ranges while readers call Bytes/Count/Verify.
// Afterwards the set must be canonical and exactly the surviving bits
// must be present. Run with -race.
func TestConcurrentSetClear(t *testing.T) {
	const (
		workers   = 8
		bitsPerW  = 2000
		clearTail = 500 // each worker clears its own tail range
	)
	s := New()
	stop := make(chan struct{})
	var readers, writers sync.WaitGroup

	// Readers must never observe a half-updated run list.
	for range 2 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = s.Bytes()
				_ = s.Count()
				if err := s.Verify(); err != nil {
					t.Errorf("concurrent Verify: %v", err)
					return
				}
			}
		}()
	}

	for w := range workers {
		writers.Add(1)
		go func(base uint32) {
			defer writers.Done()
			for i := uint32(0); i < bitsPerW; i++ {
				s.Set(base + i)
			}
			for i := uint32(0); i < clearTail; i++ {
				s.Clear(base + bitsPerW - 1 - i)
			}
		}(uint32(w) * bitsPerW)
	}

	writers.Wait()
	close(stop)
	readers.Wait()

	if err := s.Verify(); err != nil {
		t.Fatalf("post-concurrency Verify: %v", err)
	}
	want := uint64(workers * (bitsPerW - clearTail))
	if got := s.Count(); got != want {
		t.Fatalf("count = %d, want %d (lost updates?)", got, want)
	}
	for w := range workers {
		base := uint32(w) * bitsPerW
		if !s.Contains(base) || !s.Contains(base+bitsPerW-clearTail-1) {
			t.Fatalf("worker %d range missing", w)
		}
		if s.Contains(base + bitsPerW - 1) {
			t.Fatalf("worker %d cleared bit still set", w)
		}
	}
}
