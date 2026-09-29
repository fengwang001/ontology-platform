package secindex

import (
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// snapshot holds the three query kinds taken by one reader at one point.
type snapshot struct {
	lookup0 []string
	lookup1 []string
	rng     []string
}

func takeSnapshot(m *Maintainer) snapshot {
	v := m.Snapshot()
	return snapshot{lookup0: v.Lookup0, lookup1: v.Lookup1, rng: v.Range01}
}

func checkSnapshot(t *testing.T, where string, s snapshot) {
	t.Helper()
	cnt := 0
	if contains(s.lookup0, "k0") {
		cnt++
	}
	if contains(s.lookup1, "k0") {
		cnt++
	}
	if cnt != 1 {
		t.Fatalf("%s: k0 in %d value groups at once: %+v", where, cnt, s)
	}
	if !reflect.DeepEqual(s.rng, s.lookup0) {
		t.Fatalf("%s: Range(0,1)=%v != Lookup(0)=%v", where, s.rng, s.lookup0)
	}
	if !sort.StringsAreSorted(s.rng) ||
		!sort.StringsAreSorted(s.lookup0) ||
		!sort.StringsAreSorted(s.lookup1) {
		t.Fatalf("%s: result not key-sorted: %+v", where, s)
	}
}

// TestConcurrentReadersSameState runs all readers behind one start gate while
// the writer is parked between updates, so every reader observes the same
// committed state; results must then be element-wise identical.
func TestConcurrentReadersSameState(t *testing.T) {
	m := New()
	for _, key := range []string{"k0", "k1", "k2", "k3"} {
		if err := m.Upsert(key, 0); err != nil {
			t.Fatal(err)
		}
	}

	const readers = 8
	const rounds = 100
	out := make([][]snapshot, rounds)

	for r := 0; r < rounds; r++ {
		// Deterministically place k0 for this round, then park the writer.
		value := int64(r % 2)
		if err := m.Upsert("k0", value); err != nil {
			t.Fatal(err)
		}

		var start sync.WaitGroup
		var done sync.WaitGroup
		start.Add(1)
		snaps := make([]snapshot, readers)
		for i := 0; i < readers; i++ {
			done.Add(1)
			go func(idx int) {
				defer done.Done()
				start.Wait()
				snaps[idx] = takeSnapshot(m)
			}(i)
		}
		start.Done()
		done.Wait()
		out[r] = snaps

		base := snaps[0]
		checkSnapshot(t, "round 0 base", base)
		for i := 1; i < readers; i++ {
			if !reflect.DeepEqual(snaps[i], base) {
				t.Fatalf("round %d: reader %d differs element-wise from reader 0:\n %+v\n %+v",
					r, i, snaps[i], base)
			}
		}
		if err := m.VerifyByRescan(); err != nil {
			t.Fatalf("round %d rescan: %v", r, err)
		}
		if r%25 == 0 {
			t.Logf("round=%d | k0 value=%d | %d readers agree element-wise | groups=%s | rescan=nil",
				r, value, readers, formatGroups(m.IndexGroups()))
		}
	}
}

// TestConcurrentReadersAndWriters is an unsynchronized stress run (run with
// -race). It cannot demand cross-reader equality because updates may land
// between readers; instead each individual snapshot must be internally
// consistent, the maintained index must always match a full rescan, and after
// every completed update the moved key exists only in the new value group.
func TestConcurrentReadersAndWriters(t *testing.T) {
	m := New()
	for _, key := range []string{"k0", "k1", "k2", "k3"} {
		if err := m.Upsert(key, 0); err != nil {
			t.Fatal(err)
		}
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	var bad atomic.Bool

	wg.Add(1)
	go func() {
		defer wg.Done()
		v := int64(0)
		for i := 0; i < 500 && !bad.Load(); i++ {
			v = 1 - v
			if err := m.Upsert("k0", v); err != nil {
				t.Errorf("writer: %v", err)
				bad.Store(true)
				return
			}
			// After the completed Upsert returns, k0 is only in group v.
			if !contains(m.Lookup(v), "k0") || contains(m.Lookup(1-v), "k0") {
				t.Errorf("post-update invariant broken for value %d", v)
				bad.Store(true)
				return
			}
		}
		stop.Store(true)
	}()

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; !stop.Load() && !bad.Load(); i++ {
				checkSnapshot(t, "stress", takeSnapshot(m))
				if i%50 == 0 {
					if err := m.VerifyByRescan(); err != nil {
						t.Errorf("stress rescan: %v", err)
						bad.Store(true)
						return
					}
				}
			}
		}()
	}

	// Extra readers exercise the individual query entry points concurrently
	// with the writer (each call is individually linearized, so only
	// per-result ordering is asserted here).
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; !stop.Load() && !bad.Load(); i++ {
				for _, res := range [][]string{
					m.Lookup(int64((i + seed) % 3)),
					m.Range(-1, 2),
					m.Range(0, 0),
				} {
					if !sort.StringsAreSorted(res) {
						t.Errorf("concurrent query returned unsorted result: %v", res)
						bad.Store(true)
						return
					}
				}
			}
		}(r)
	}

	wg.Wait()
	if bad.Load() {
		t.Fatal("stress run reported failures above")
	}
	if err := m.Upsert("k0", 1); err != nil {
		t.Fatal(err)
	}
	final0, final1 := m.Lookup(0), m.Lookup(1)
	if !contains(final1, "k0") || contains(final0, "k0") {
		t.Fatalf("final placement wrong: group0=%v group1=%v", final0, final1)
	}
	if err := m.VerifyByRescan(); err != nil {
		t.Fatalf("final rescan: %v", err)
	}
	t.Logf("stress done | final groups=%s | rescan=nil | verdict=PASS",
		formatGroups(m.IndexGroups()))
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
