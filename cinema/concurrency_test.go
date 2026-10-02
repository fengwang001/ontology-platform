package cinema

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// Concurrent Hold/Confirm/Release/Seats calls must behave as some
// serial execution: ids stay gapless, no seat is doubly occupied and
// the seat counts stay consistent.
func TestConcurrentOps(t *testing.T) {
	const R, W = 8, 20
	r := mustRegistry(t, R, W, 5)

	var clock atomic.Int64
	var successHolds atomic.Int64
	var idsMu sync.Mutex
	var issued []int

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 400; i++ {
				if rng.Intn(3) == 0 {
					clock.Add(1)
				}
				now := clock.Load()
				switch rng.Intn(10) {
				case 0, 1, 2, 3, 4:
					id, _, _, err := r.Hold(1+rng.Intn(8), now)
					if err == nil {
						successHolds.Add(1)
						idsMu.Lock()
						issued = append(issued, id)
						idsMu.Unlock()
					}
				case 5, 6, 7:
					idsMu.Lock()
					var id int
					if len(issued) > 0 {
						id = issued[rng.Intn(len(issued))]
					}
					idsMu.Unlock()
					_ = r.Confirm(id, now)
				case 8:
					idsMu.Lock()
					var id int
					if len(issued) > 0 {
						id = issued[rng.Intn(len(issued))]
					}
					idsMu.Unlock()
					_ = r.Release(id, now)
				default:
					_ = r.Seats(now)
				}
			}
		}(int64(g)*977 + 1)
	}
	wg.Wait()

	// Exactly one id per successful hold, gapless from 1.
	if got, want := int(successHolds.Load()), r.nextID-1; got != want {
		t.Fatalf("successful holds %d != issued ids %d", got, want)
	}
	if len(r.holds) != r.nextID-1 {
		t.Fatalf("hold records %d != nextID-1 %d", len(r.holds), r.nextID-1)
	}
	now := clock.Load()
	// No seat occupied by two holds; counts sum to R*W.
	cover := make([][]int, R)
	for i := range cover {
		cover[i] = make([]int, W)
	}
	for _, h := range r.holds {
		if _, occ := h.statusAt(now); !occ {
			continue
		}
		for c := h.start; c < h.start+h.k; c++ {
			cover[h.row-1][c-1]++
			if cover[h.row-1][c-1] > 1 {
				t.Fatalf("seat (%d,%d) doubly occupied", h.row, c)
			}
		}
	}
	var n [3]int
	for _, row := range r.Seats(now) {
		for _, st := range row {
			n[st]++
		}
	}
	if n[0]+n[1]+n[2] != R*W {
		t.Fatalf("counts %v do not sum to %d", n, R*W)
	}
	t.Logf("final: holds=%d free=%d held=%d confirmed=%d", len(r.holds), n[0], n[1], n[2])
}
