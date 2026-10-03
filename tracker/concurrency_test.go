package tracker

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentOps hammers one tracker from many goroutines; under -race
// this validates the locking, and the final revision must equal the number
// of successful edits (i.e. results equal some serial order).
func TestConcurrentOps(t *testing.T) {
	tr := NewTracker(20, 1<<20, 256)
	var edits atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			ids := make([]int, 0, 16)
			for i := 0; i < 300; i++ {
				L := tr.Len()
				switch rng.Intn(6) {
				case 0:
					p := rng.Intn(L + 1)
					d := rng.Intn(L - p + 1)
					n := rng.Intn(4)
					if d+n == 0 {
						n = 1
					}
					if _, err := tr.Replace(p, d, n); err == nil {
						edits.Add(1)
					}
				case 1:
					if L == 0 {
						continue
					}
					p := rng.Intn(L)
					length := 1 + rng.Intn(L-p)
					q := rng.Intn(L + 1)
					if _, err := tr.Move(p, length, q); err == nil {
						edits.Add(1)
					}
				case 2:
					if id, err := tr.AddPoint(rng.Intn(L+1), Bias(rng.Intn(2))); err == nil {
						ids = append(ids, id)
					}
				case 3:
					a := rng.Intn(L + 1)
					b := rng.Intn(L + 1)
					if a > b {
						a, b = b, a
					}
					if id, err := tr.AddRange(a, b, RangeKind(rng.Intn(2))); err == nil {
						ids = append(ids, id)
					}
				case 4:
					if len(ids) > 0 {
						j := rng.Intn(len(ids))
						tr.Remove(ids[j])
						ids = append(ids[:j], ids[j+1:]...)
					}
				case 5:
					rev := tr.Rev()
					tr.Compact(rng.Intn(rev + 1))
				}
				if len(ids) > 0 {
					id := ids[rng.Intn(len(ids))]
					asRev := rng.Intn(tr.Rev() + 1)
					if pos, err := tr.Pos(id, asRev); err == nil {
						if pos < 0 || pos > 1<<20 {
							t.Errorf("Pos(%d, %d) = %d out of bounds", id, asRev, pos)
						}
					}
					if s, e, col, err := tr.Range(id, asRev); err == nil {
						if s < 0 || e > 1<<20 || s > e || col != (s == e) {
							t.Errorf("Range(%d, %d) = (%d,%d,%v) invalid", id, asRev, s, e, col)
						}
					}
				}
			}
		}(int64(g*1000 + 7))
	}
	wg.Wait()
	if got := tr.Rev(); got != int(edits.Load()) {
		t.Fatalf("final rev = %d, want %d (successful edits)", got, edits.Load())
	}
}
