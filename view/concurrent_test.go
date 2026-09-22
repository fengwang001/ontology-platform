package view_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentSubmit runs many writers while a reader continuously
// snapshots the view. Every observed snapshot must be internally
// consistent (never "Count updated while Sum is not") and the final state
// must equal the expected per-group counts. Run under -race this also
// checks the locking discipline.
func TestConcurrentSubmit(t *testing.T) {
	v := newTestView(t)
	const writers = 8
	const perWriter = 500

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Reader: snapshots must always be coherent.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				for _, g := range v.Groups() {
					if g.CountExists != g.SumExists || g.CountExists != g.MinExists {
						t.Errorf("observed half-updated group: %+v", g)
						return
					}
					if g.CountExists {
						// In this workload every value is 1, so a coherent
						// group has Sum == Count and Min == Max == 1.
						if g.Sum != g.Count || g.Min != 1 || g.Max != 1 {
							t.Errorf("incoherent group snapshot: %+v", g)
							return
						}
					}
				}
			}
		}
	}()

	var submitWg sync.WaitGroup
	var ver atomic.Uint64
	var order sync.Mutex
	for w := 0; w < writers; w++ {
		submitWg.Add(1)
		go func(w int) {
			defer submitWg.Done()
			for i := 0; i < perWriter; i++ {
				n := w*perWriter + i
				group := fmt.Sprintf("g%d", n%4)
				order.Lock()
				nv := ver.Add(1)
				err := v.Submit(ins(nv, fmt.Sprintf("k%05d", n), group, 1))
				order.Unlock()
				if err != nil {
					t.Errorf("submit: %v", err)
					return
				}
			}
		}(w)
	}
	submitWg.Wait()
	close(stop)
	wg.Wait()

	var total float64
	for _, name := range v.GroupNames() {
		g, ok := v.Lookup(name)
		if !ok {
			t.Fatal("group vanished from listing")
		}
		total += g.Count
	}
	if total != writers*perWriter {
		t.Fatalf("total count=%v want %d (lost concurrent write)",
			total, writers*perWriter)
	}
}

// TestConcurrentRecomputeNoLoss hammers one group with concurrent writes
// while repeatedly deleting its current minimum, forcing recomputes. No
// surviving member may be lost.
func TestConcurrentRecomputeNoLoss(t *testing.T) {
	v := newTestView(t)
	var ver atomic.Uint64
	for i := 1; i <= 200; i++ {
		if err := v.Submit(ins(uint64(i), fmt.Sprintf("init%03d", i), "g", float64(i))); err != nil {
			t.Fatal(err)
		}
	}
	ver.Store(200)

	const writers = 4
	var wg sync.WaitGroup
	var order sync.Mutex
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				key := fmt.Sprintf("w%dk%d", w, i)
				order.Lock()
				insVer := ver.Add(1)
				err1 := v.Submit(ins(insVer, key, "g", 0.5))
				delVer := ver.Add(1)
				err2 := v.Submit(del(delVer, key, "g", 0.5))
				order.Unlock()
				if err1 != nil || err2 != nil {
					t.Errorf("insert/delete: %v %v", err1, err2)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	g, ok := v.Lookup("g")
	if !ok {
		t.Fatal("group missing")
	}
	if g.Count != 200 {
		t.Fatalf("count=%v want 200 (writes lost during recompute)", g.Count)
	}
	if g.Min != 1 {
		t.Fatalf("min=%v want 1", g.Min)
	}
	if g.Max != 200 {
		t.Fatalf("max=%v want 200", g.Max)
	}
	if t.Failed() {
		t.Fatal("concurrent recompute failed")
	}
}
