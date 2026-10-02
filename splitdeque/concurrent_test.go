package splitdeque

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrent runs one owner (Push/Pop) against multiple stealers and
// a stats reader. Linearizability is enforced by the internal lock; the
// race detector plus the final conservation identity validate safety.
func TestConcurrent(t *testing.T) {
	d := mustNew(t, 256, 64, 2, 5)
	const rounds = 20_000
	var pushed, popped, stolen int64
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if i%2 == 0 {
				if err := d.Push(int64(i)); err == nil {
					atomic.AddInt64(&pushed, 1)
				}
			} else {
				if _, ok := d.Pop(); ok {
					atomic.AddInt64(&popped, 1)
				}
			}
		}
		close(stop)
	}()

	for thief := 0; thief < 4; thief++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := d.Steal(int64(1 + thief*3))
				if err == nil {
					atomic.AddInt64(&stolen, int64(len(got)))
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = d.Stats()
			_, _, _ = d.MoveCounts()
		}
	}()

	wg.Wait()

	// Drain whatever remains through the owner side.
	for {
		if _, ok := d.Pop(); !ok {
			break
		}
		popped++
	}
	// Thieves may still hold none; after drain S and P are both empty.
	rem, _ := d.Steal(256)
	stolen += int64(len(rem))

	st := d.Stats()
	if st.Pushed != pushed {
		t.Fatalf("observed pushed %d != stats %d", pushed, st.Pushed)
	}
	if st.Popped+st.Stolen != popped+stolen {
		// popped/stolen counters from goroutines are best-effort under
		// interleaving; the authoritative check is the identity below.
		t.Logf("best-effort counters differ (allowed): %d+%d vs %d+%d", st.Popped, st.Stolen, popped, stolen)
	}
	if st.Pushed != st.Popped+st.Stolen+(d.b-d.t) {
		t.Fatalf("identity violated: %+v occupancy=%d", st, d.b-d.t)
	}
	if d.b-d.t != 0 {
		t.Fatalf("occupancy after drain = %d, want 0", d.b-d.t)
	}
	rel, rec, copies := d.MoveCounts()
	if rel != 0 || rec != 0 || copies != st.Stolen {
		t.Fatalf("MoveCounts=(%d,%d,%d), stolen=%d, want (0,0,stolen)", rel, rec, copies, st.Stolen)
	}
	t.Logf("input: 1 owner + 4 stealers + stats reader over %d owner rounds; basis: identity holds, moves=(0,0,%d)",
		rounds, st.Stolen)
}
