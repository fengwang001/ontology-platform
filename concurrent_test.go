package ontology

import (
	"bytes"
	"sync"
	"testing"
)

const (
	concWorkers  = 8
	concBitsEach = 2000
)

// workerRange returns the disjoint bit range owned by worker w.
func workerRange(w int) (uint32, uint32) {
	start := uint32(w) * concBitsEach
	return start, start + concBitsEach
}

func TestConcurrentSetClear(t *testing.T) {
	s := New()
	var wg sync.WaitGroup

	// Readers must never observe a half-updated run list.
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	for i := 0; i < 2; i++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = s.Bytes()
				_ = s.Count()
				if !s.Verify() {
					t.Error("observed non-normalized run list")
					return
				}
			}
		}()
	}

	// Phase 1: concurrent disjoint Sets.
	for w := 0; w < concWorkers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			lo, hi := workerRange(w)
			for p := lo; p < hi; p++ {
				s.Set(p)
			}
		}(w)
	}
	wg.Wait()
	if got := s.Count(); got != concWorkers*concBitsEach {
		t.Fatalf("lost updates: Count = %d, want %d", got, concWorkers*concBitsEach)
	}

	// Phase 2: concurrent Clears of every second bit.
	for w := 0; w < concWorkers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			lo, hi := workerRange(w)
			for p := lo; p < hi; p += 2 {
				s.Clear(p)
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	rwg.Wait()

	if !s.Verify() {
		t.Fatal("Verify failed after concurrent mutation")
	}
	if got := s.Count(); got != concWorkers*concBitsEach/2 {
		t.Fatalf("Count = %d, want %d", got, concWorkers*concBitsEach/2)
	}

	// The result must equal a sequentially built reference byte-for-byte.
	ref := New()
	for w := 0; w < concWorkers; w++ {
		lo, hi := workerRange(w)
		for p := lo + 1; p < hi; p += 2 {
			ref.Set(p)
		}
	}
	if !bytes.Equal(s.Bytes(), ref.Bytes()) {
		t.Fatal("concurrent result encoding differs from sequential reference")
	}
}
