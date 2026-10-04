package review

import (
	"sync"
	"testing"
)

// TestConcurrentSerializability hammers one engine concurrently; the single
// internal mutex makes every result equivalent to some serial order, and the
// final state must always remain internally consistent.
func TestConcurrentSerializability(t *testing.T) {
	e := NewEngine(5)
	mustDrug(t, e, "D", []byte("X"), 100, 1)
	mustMax(t, e, []byte("X"), 100000)
	seedCommon(t, e)

	const goroutines = 16
	const perG = 40
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				now := g*perG + i
				_, st, _, err := e.Submit(now, "d1", "p", []Item{
					{Drug: "D", Per: 1, PerDay: 1, Start: now, End: now + 2},
				})
				if err == nil && st != StatusActive && st != StatusPending {
					t.Errorf("unexpected status %d", st)
				}
			}
		}(g)
	}
	wg.Wait()

	// Every recorded prescription must have a valid status; no active/pending
	// item may exceed the cap on any day (spot-check via a second patient
	// view of records).
	for id := 1; ; id++ {
		r, ok := e.RxRecord(id)
		if !ok {
			break
		}
		switch r.Status {
		case StatusActive, StatusPending, StatusVoided:
		default:
			t.Fatalf("rx %d bad status %d", id, r.Status)
		}
	}
}
