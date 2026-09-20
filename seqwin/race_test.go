package seqwin

import (
	"sync"
	"sync/atomic"
	"testing"
)

// Semantics 8: concurrent Accept over the same set of sequence
// numbers must be race-free, judge each sequence number Fresh exactly
// once across all goroutines (the rest Duplicate), and leave Highest
// at the maximum. The window is sized to cover every sequence number
// used, so no goroutine can legitimately observe TooOld.
func TestConcurrentAccept(t *testing.T) {
	const goroutines = 8
	const maxSeq = 5000

	w := New(maxSeq)
	fresh := make([]atomic.Int64, maxSeq+1)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seq := uint64(1); seq <= maxSeq; seq++ {
				switch w.Accept(seq) {
				case Fresh:
					fresh[seq].Add(1)
				case Duplicate:
				default:
					t.Errorf("Accept(%d) returned unexpected verdict", seq)
				}
			}
		}()
	}
	wg.Wait()

	for seq := uint64(1); seq <= maxSeq; seq++ {
		if n := fresh[seq].Load(); n != 1 {
			t.Errorf("seq %d judged Fresh %d times, want exactly 1", seq, n)
		}
	}
	if w.Highest() != maxSeq {
		t.Errorf("Highest() = %d, want %d", w.Highest(), maxSeq)
	}
}
