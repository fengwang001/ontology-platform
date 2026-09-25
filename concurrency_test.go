package ontology

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentUnionsMergeIntoOneClass hammers the set with concurrent
// unions whose edges form a ring over n elements. No merge may be lost:
// at the end exactly one class must remain.
func TestConcurrentUnionsMergeIntoOneClass(t *testing.T) {
	const n = 2_000
	const workers = 16
	var ds DisjointSet
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < n; i += workers {
				ds.Union(chainID(i), chainID((i+1)%n))
			}
		}(w)
	}
	wg.Wait()

	if got := ds.ClassCount(); got != 1 {
		t.Fatalf("ClassCount after concurrent unions = %d, want 1 (lost merge?)", got)
	}
	rep, err := ds.Find(chainID(0))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, spot := range []int{0, n / 3, n / 2, n - 1} {
		got, err := ds.Find(chainID(spot))
		if err != nil {
			t.Fatalf("Find(%q): %v", chainID(spot), err)
		}
		if got != rep {
			t.Fatalf("Find(%q) = %q, want %q", chainID(spot), got, rep)
		}
	}
}

// TestConnectedNeverRegresses runs concurrent readers against a pair of
// elements while a writer unions everything into one class. Once
// Connected reports true it must never report false again: readers must
// not observe a half-finished merge.
func TestConnectedNeverRegresses(t *testing.T) {
	const n = 500
	var ds DisjointSet
	for i := 0; i < n; i++ {
		ds.Add(chainID(i))
	}

	var regressed atomic.Bool
	var stop atomic.Bool
	var wg sync.WaitGroup

	// Readers: poll Connected(first, last); once true, false is a bug.
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seenTrue := false
			for !stop.Load() {
				ok, err := ds.Connected(chainID(0), chainID(n-1))
				if err != nil {
					t.Errorf("Connected on known IDs: %v", err)
					return
				}
				if ok {
					seenTrue = true
				} else if seenTrue {
					regressed.Store(true)
					return
				}
			}
		}()
	}

	// Readers: a class only grows, so its representative (the smallest
	// member) is monotonically non-increasing over time.
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			last := chainID(n - 1)
			for !stop.Load() {
				rep, err := ds.Find(chainID(n - 1))
				if err != nil {
					t.Errorf("Find on known ID: %v", err)
					return
				}
				if rep > last {
					t.Errorf("representative rose from %q to %q mid-merge", last, rep)
					return
				}
				last = rep
			}
		}()
	}

	// Writer: chain everything into one class.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i+1 < n; i++ {
			ds.Union(chainID(i), chainID(i+1))
		}
		stop.Store(true)
	}()
	wg.Wait()

	if regressed.Load() {
		t.Fatal("Connected regressed from true to false during concurrent merges")
	}
	ok, err := ds.Connected(chainID(0), chainID(n-1))
	if err != nil || !ok {
		t.Fatalf("final Connected = %v, %v; want true, nil", ok, err)
	}
	if got := ds.ClassCount(); got != 1 {
		t.Fatalf("ClassCount = %d, want 1", got)
	}
}
