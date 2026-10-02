package deadlock

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentHammer calls every operation concurrently. Each individual
// call is linearized by the detector mutex; the externally observable result
// is that invariants always hold and after the workload finishes no blocked
// process has a fitting alternative. Run with -race.
func TestConcurrentHammer(t *testing.T) {
	const workers = 16
	d := newTestD(t, 3, []int64{4, 4, 4}, []int64{1, 2, 3}, workers, 3)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < 400; i++ {
				switch i % 5 {
				case 0:
					_, _ = d.Request(p, [][]int64{{1, 0, 0}, {0, 1, 0}})
				case 1:
					d.Detect()
				case 2:
					d.Resolve()
				case 3:
					snap := d.Snapshot()
					vec := []int64{0, 0, 0}
					for r := range vec {
						if snap.Alloc[p][r] > 0 {
							vec[r] = 1
						}
					}
					if vec[0]+vec[1]+vec[2] > 0 {
						_, _ = d.Release(p, vec)
					}
				case 4:
					_, _ = d.Request(p, [][]int64{{0, 0, 1}})
				}
			}
		}(w)
	}
	wg.Wait()

	// Conservation.
	sum := append([]int64(nil), d.avail...)
	for p := 0; p < d.pnum; p++ {
		if !d.alive[p] {
			continue
		}
		for r, v := range d.alloc[p] {
			sum[r] += v
			if v < 0 {
				t.Fatalf("negative alloc p%d", p)
			}
		}
	}
	for r, v := range sum {
		if v != d.total[r] {
			t.Fatalf("conservation r%d: %d != %d", r, v, d.total[r])
		}
	}
	// Blocked means genuinely unfit; rb bound for survivors.
	for p := 0; p < d.pnum; p++ {
		if d.alive[p] && d.rb[p] >= d.rollback {
			t.Fatalf("alive p%d rb=%d", p, d.rb[p])
		}
		if d.blocked[p] {
			for _, alt := range d.alts[p] {
				if vecFits(alt, d.avail) {
					t.Fatalf("blocked p%d with fitting alt %v", p, alt)
				}
			}
		}
	}
	// Deterministic replay: same operation script twice yields equal traces.
	script := func(d2 *Detector) []string {
		var trace []string
		for _, p := range []int{0, 1, 2} {
			g, err := d2.Request(p, [][]int64{{1, 1, 0}})
			trace = append(trace, fmt.Sprintf("req%d %v %v", p, g, err))
		}
		trace = append(trace, fmt.Sprintf("detect %v", d2.Detect()))
		trace = append(trace, fmt.Sprintf("resolve %+v", d2.Resolve()))
		gr, err := d2.Release(0, []int64{1, 1, 0})
		trace = append(trace, fmt.Sprintf("rel %v %v", gr, err))
		return trace
	}
	a := newTestD(t, 3, []int64{4, 4, 4}, []int64{1, 2, 3}, workers, 3)
	b := newTestD(t, 3, []int64{4, 4, 4}, []int64{1, 2, 3}, workers, 3)
	if ta, tb := script(a), script(b); fmt.Sprint(ta) != fmt.Sprint(tb) {
		t.Fatalf("nondeterministic replay:\n%v\n%v", ta, tb)
	}
}
