package ontology

import (
	"errors"
	"math"
	"strconv"
	"sync"
	"testing"
)

func mustCall(t *testing.T, f func() (int64, error)) int64 {
	t.Helper()
	seq, err := f()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return seq
}

func checkGet(t *testing.T, c *Compactor, k string, s uint64, wantV int64, wantOK bool) {
	t.Helper()
	gotV, gotOK, err := c.Get(k, s)
	if err != nil {
		t.Fatalf("Get(%q,%d): %v", k, s, err)
	}
	if gotOK != wantOK || (gotOK && gotV != wantV) {
		t.Fatalf("Get(%q,%d) = (%d,%v), want (%d,%v)", k, s, gotV, gotOK, wantV, wantOK)
	}
}

func checkConservation(t *testing.T, st Stats) {
	t.Helper()
	if st.In != st.Out+st.Shadowed+st.Folded+st.TombDropped {
		t.Fatalf("conservation violated: %+v", st)
	}
}

// TestExampleOne mirrors 例一.
func TestExampleOne(t *testing.T) {
	c := New(nil)
	mustCall(t, func() (int64, error) { return c.Put("a", 10) })
	mustCall(t, func() (int64, error) { return c.Merge("a", 5) })
	mustCall(t, func() (int64, error) { return c.Merge("a", 7) })
	s := c.Snapshot() // 3
	if s != 3 {
		t.Fatalf("snapshot = %d", s)
	}
	mustCall(t, func() (int64, error) { return c.Delete("a") })
	mustCall(t, func() (int64, error) { return c.Merge("a", 1) })

	checkGet(t, c, "a", uint64(3), 22, true)
	checkGet(t, c, "a", Latest, 1, true)

	st := c.Compact()
	if st != (Stats{In: 5, Out: 2, Folded: 3}) {
		t.Fatalf("stats = %+v", st)
	}
	checkConservation(t, st)

	rs := c.Records()
	if len(rs) != 2 {
		t.Fatalf("records = %s", recsString(rs))
	}
	if rs[0] != (Record{"a", 3, KindPut, 22}) || rs[1] != (Record{"a", 5, KindPut, 1}) {
		t.Fatalf("records = %s", recsString(rs))
	}
	checkGet(t, c, "a", uint64(3), 22, true)
	checkGet(t, c, "a", Latest, 1, true)
}

// TestExampleTwo mirrors 例二 with and without a deeper base.
func TestExampleTwo(t *testing.T) {
	c := New(nil)
	mustCall(t, func() (int64, error) { return c.Put("b", 4) })
	mustCall(t, func() (int64, error) { return c.Delete("b") })
	st := c.Compact()
	if st != (Stats{In: 2, Out: 0, Shadowed: 1, TombDropped: 1}) {
		t.Fatalf("stats = %+v", st)
	}
	checkConservation(t, st)
	checkGet(t, c, "b", Latest, 0, false)
	if rs := c.Records(); len(rs) != 0 {
		t.Fatalf("records = %s", recsString(rs))
	}

	c = New(map[string]int64{"b": 9})
	mustCall(t, func() (int64, error) { return c.Put("b", 4) })
	mustCall(t, func() (int64, error) { return c.Delete("b") })
	st = c.Compact()
	if st != (Stats{In: 2, Out: 1, Shadowed: 1}) {
		t.Fatalf("stats = %+v", st)
	}
	checkConservation(t, st)
	checkGet(t, c, "b", Latest, 0, false)
	rs := c.Records()
	if len(rs) != 1 || rs[0] != (Record{"b", 2, KindDelete, 0}) {
		t.Fatalf("records = %s", recsString(rs))
	}
}

// TestStripeBoundary: a record at exactly a snapshot number belongs to
// that snapshot's stripe, not the newer one.
func TestStripeBoundary(t *testing.T) {
	c := New(nil)
	mustCall(t, func() (int64, error) { return c.Merge("k", 2) })
	s1 := c.Snapshot() // == 1
	mustCall(t, func() (int64, error) { return c.Merge("k", 3) })

	checkGet(t, c, "k", uint64(s1), 2, true)
	checkGet(t, c, "k", Latest, 5, true)
	st := c.Compact()
	if st != (Stats{In: 2, Out: 2, MergeToPut: 1}) {
		t.Fatalf("stats = %+v", st)
	}
	checkConservation(t, st)
	rs := c.Records()
	if len(rs) != 2 || rs[0] != (Record{"k", 1, KindPut, 2}) || rs[1] != (Record{"k", 2, KindMerge, 3}) {
		t.Fatalf("records = %s", recsString(rs))
	}
	checkGet(t, c, "k", uint64(s1), 2, true)
	checkGet(t, c, "k", Latest, 5, true)
}

// TestDuplicateHoldsMergeStripes: one sequence held twice is one stripe.
func TestDuplicateHoldsMergeStripes(t *testing.T) {
	c := New(nil)
	mustCall(t, func() (int64, error) { return c.Merge("k", 2) })
	s := c.Snapshot() // first hold at 1
	c.Snapshot()      // second hold at 1
	mustCall(t, func() (int64, error) { return c.Merge("k", 3) })

	st := c.Compact()
	if st != (Stats{In: 2, Out: 2, MergeToPut: 1}) {
		t.Fatalf("stats = %+v", st)
	}
	checkConservation(t, st)

	if err := c.Release(s); err != nil {
		t.Fatal(err)
	}
	st = c.Compact()
	if st != (Stats{In: 2, Out: 2}) {
		t.Fatalf("after first release stats = %+v", st)
	}
	if err := c.Release(s); err != nil {
		t.Fatal(err)
	}
	st = c.Compact()
	if st != (Stats{In: 2, Out: 1, Folded: 1}) {
		t.Fatalf("after second release stats = %+v", st)
	}
	checkConservation(t, st)
	rs := c.Records()
	if len(rs) != 1 || rs[0] != (Record{"k", 2, KindPut, 5}) {
		t.Fatalf("records = %s", recsString(rs))
	}
	checkGet(t, c, "k", Latest, 5, true)
}

// TestDeleteThenMerge: Merge above Delete becomes Put(sum).
func TestDeleteThenMerge(t *testing.T) {
	c := New(map[string]int64{"k": 100})
	mustCall(t, func() (int64, error) { return c.Delete("k") })
	mustCall(t, func() (int64, error) { return c.Merge("k", 7) })
	checkGet(t, c, "k", Latest, 7, true)
	st := c.Compact()
	if st != (Stats{In: 2, Out: 1, Folded: 1}) {
		t.Fatalf("stats = %+v", st)
	}
	checkConservation(t, st)
	rs := c.Records()
	if len(rs) != 1 || rs[0] != (Record{"k", 2, KindPut, 7}) {
		t.Fatalf("records = %s", recsString(rs))
	}
	checkGet(t, c, "k", Latest, 7, true)
}

// TestMergeNoBaseRefold: a base-less merge survives compaction and
// refolds when stripes merge after a release.
func TestMergeNoBaseRefold(t *testing.T) {
	c := New(nil)
	mustCall(t, func() (int64, error) { return c.Merge("k", 2) })
	s := c.Snapshot()
	mustCall(t, func() (int64, error) { return c.Merge("k", 3) })
	st := c.Compact()
	if st != (Stats{In: 2, Out: 2, MergeToPut: 1}) {
		t.Fatalf("first compact stats = %+v", st)
	}
	checkConservation(t, st)

	mustCall(t, func() (int64, error) { return c.Merge("k", 4) })
	st = c.Compact()
	if st != (Stats{In: 3, Out: 2, Folded: 1}) {
		t.Fatalf("second compact stats = %+v", st)
	}
	checkConservation(t, st)
	checkGet(t, c, "k", uint64(s), 2, true)
	checkGet(t, c, "k", Latest, 9, true)

	if err := c.Release(s); err != nil {
		t.Fatal(err)
	}
	st = c.Compact()
	if st != (Stats{In: 2, Out: 1, Folded: 1}) {
		t.Fatalf("third compact stats = %+v", st)
	}
	checkConservation(t, st)
	rs := c.Records()
	if len(rs) != 1 || rs[0] != (Record{"k", 3, KindPut, 9}) {
		t.Fatalf("records = %s", recsString(rs))
	}
}

// TestTrailingTombsThenMerge: tombstones across stripes drop oldest-first
// repeatedly, then the remaining oldest merge becomes a Put.
func TestTrailingTombsThenMerge(t *testing.T) {
	c := New(nil)
	mustCall(t, func() (int64, error) { return c.Merge("k", 1) })
	s1 := c.Snapshot()
	mustCall(t, func() (int64, error) { return c.Delete("k") })
	s2 := c.Snapshot()
	mustCall(t, func() (int64, error) { return c.Delete("k") })
	s3 := c.Snapshot()
	mustCall(t, func() (int64, error) { return c.Delete("k") })

	checkGet(t, c, "k", uint64(s1), 1, true)
	checkGet(t, c, "k", uint64(s2), 0, false)
	checkGet(t, c, "k", uint64(s3), 0, false)
	checkGet(t, c, "k", Latest, 0, false)

	st := c.Compact()
	if st != (Stats{In: 4, Out: 4, MergeToPut: 1}) {
		t.Fatalf("stats = %+v", st)
	}
	checkConservation(t, st)
	checkGet(t, c, "k", uint64(s1), 1, true)
	checkGet(t, c, "k", uint64(s2), 0, false)
	checkGet(t, c, "k", uint64(s3), 0, false)
	checkGet(t, c, "k", Latest, 0, false)
}

// TestMultipleTombDropsThenMergeToPut: the finishing pass drops several
// consecutive trailing tombstones in the oldest stripe, then converts the
// newly exposed oldest Merge into a Put.
func TestMultipleTombDropsThenMergeToPut(t *testing.T) {
	c := New(nil)
	mustCall(t, func() (int64, error) { return c.Merge("k", 1) }) // 1
	mustCall(t, func() (int64, error) { return c.Merge("k", 2) }) // 2
	s1 := c.Snapshot()                                            // 2
	mustCall(t, func() (int64, error) { return c.Delete("k") })   // 3
	mustCall(t, func() (int64, error) { return c.Delete("k") })   // 4
	mustCall(t, func() (int64, error) { return c.Delete("k") })   // 5

	checkGet(t, c, "k", uint64(s1), 3, true)
	checkGet(t, c, "k", Latest, 0, false)

	st := c.Compact()
	// Latest stripe: Delete@5 produced, Delete@4/@3 shadowed.
	// stripe 2: Merge@2 absorbs Merge@1 (Folded 1) -> Merge(3)@2,
	// then converted to Put.
	if st != (Stats{In: 5, Out: 2, Shadowed: 2, Folded: 1, MergeToPut: 1}) {
		t.Fatalf("stats = %+v", st)
	}
	checkConservation(t, st)
	rs := c.Records()
	if len(rs) != 2 || rs[0] != (Record{"k", 2, KindPut, 3}) || rs[1] != (Record{"k", 5, KindDelete, 0}) {
		t.Fatalf("records = %s", recsString(rs))
	}
	checkGet(t, c, "k", uint64(s1), 3, true)
	checkGet(t, c, "k", Latest, 0, false)

	// After releasing the snapshot only the Latest stripe remains:
	// Delete@5 shadows everything older; the trailing tomb is then
	// dropped repeatedly (only one Delete record remains at that point,
	// and Put@3 becomes the oldest output and must not be removed).
	if err := c.Release(s1); err != nil {
		t.Fatal(err)
	}
	st = c.Compact()
	if st != (Stats{In: 2, Out: 0, Shadowed: 1, TombDropped: 1}) {
		t.Fatalf("second compact stats = %+v", st)
	}
	checkConservation(t, st)
	if rs = c.Records(); len(rs) != 0 {
		t.Fatalf("records = %s", recsString(rs))
	}
	checkGet(t, c, "k", Latest, 0, false)
}

// TestDeeperKeepsTombAndMerge: deeper presence preserves the oldest
// tomb and leaves the oldest merge as a merge.
func TestDeeperKeepsTombAndMerge(t *testing.T) {
	c := New(map[string]int64{"k": 9})
	mustCall(t, func() (int64, error) { return c.Merge("k", 1) })
	s := c.Snapshot()
	mustCall(t, func() (int64, error) { return c.Delete("k") })
	st := c.Compact()
	if st != (Stats{In: 2, Out: 2}) {
		t.Fatalf("stats = %+v", st)
	}
	checkConservation(t, st)
	rs := c.Records()
	if len(rs) != 2 || rs[0] != (Record{"k", 1, KindMerge, 1}) || rs[1] != (Record{"k", 2, KindDelete, 0}) {
		t.Fatalf("records = %s", recsString(rs))
	}
	checkGet(t, c, "k", uint64(s), 10, true)
	checkGet(t, c, "k", Latest, 0, false)
}

// TestDeeperZeroBase: zero-valued presence still counts.
func TestDeeperZeroBase(t *testing.T) {
	c := New(map[string]int64{"k": 0})
	checkGet(t, c, "k", Latest, 0, true)
}

// TestValidation checks error rules and that rejected operations
// neither consume sequences nor mutate state.
func TestValidation(t *testing.T) {
	c := New(nil)
	if _, err := c.Put("", 1); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty put: %v", err)
	}
	if _, err := c.Merge("", 1); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty merge: %v", err)
	}
	if _, err := c.Delete(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty delete: %v", err)
	}
	if _, _, err := c.Get("", Latest); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty get: %v", err)
	}
	// Empty key is checked before range.
	if _, err := c.Put("", math.MaxInt64); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want empty-first: %v", err)
	}
	bad := int64(1_000_000_000_001)
	if _, err := c.Put("k", bad); !errors.Is(err, ErrRange) {
		t.Fatalf("put range: %v", err)
	}
	if _, err := c.Merge("k", -bad); !errors.Is(err, ErrRange) {
		t.Fatalf("merge range: %v", err)
	}
	// Boundary values are accepted.
	mustCall(t, func() (int64, error) { return c.Put("k", 1_000_000_000_000) })
	mustCall(t, func() (int64, error) { return c.Merge("k", -1_000_000_000_000) })
	if got := c.Snapshot(); got != 2 {
		t.Fatalf("snapshot after rejected ops = %d", got)
	}
	if _, _, err := c.Get("k", 5); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("get unheld: %v", err)
	}
	if err := c.Release(7); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("release unheld: %v", err)
	}
	checkGet(t, c, "k", Latest, 0, true)
}

// TestEmptySnapshotReads: snapshot 0 is a valid hold until released.
func TestEmptySnapshotReads(t *testing.T) {
	c := New(map[string]int64{"d": 4})
	s := c.Snapshot()
	if s != 0 {
		t.Fatalf("snapshot = %d", s)
	}
	checkGet(t, c, "d", 0, 4, true)
	checkGet(t, c, "x", 0, 0, false)
	if err := c.Release(s); err != nil {
		t.Fatal(err)
	}
	if err := c.Release(s); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("second release: %v", err)
	}
	if _, _, err := c.Get("d", 0); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("get after release: %v", err)
	}
}

// TestStripeProbeBudget verifies the binary-search accounting bound.
func TestStripeProbeBudget(t *testing.T) {
	c := New(nil)
	for i := 0; i < 60; i++ {
		mustCall(t, func() (int64, error) { return c.Put("k", 1) })
		c.Snapshot()
	}
	live := len(c.heldSeqs())
	if live != 60 {
		t.Fatalf("holds = %d", live)
	}
	before := c.stripeProbes
	st := c.Compact()
	used := c.stripeProbes - before
	budget := probeBudget(st.In, live)
	if used > budget {
		t.Fatalf("probes used %d > budget %d for In=%d |S|=%d", used, budget, st.In, live)
	}
	checkConservation(t, st)
}

// TestConcurrent hammers every method under -race and checks the
// observable results stay consistent.
func TestConcurrent(t *testing.T) {
	c := New(map[string]int64{"d0": 0})
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				k := fmtKey(w, i)
				_, _ = c.Put(k, 1)
				_, _ = c.Merge(k, 1)
				s := c.Snapshot()
				_, _, _ = c.Get(k, uint64(s))
				_, _, _ = c.Get(k, Latest)
				_ = c.Records()
				_ = c.Compact()
				if err := c.Release(s); err != nil {
					t.Errorf("release: %v", err)
					return
				}
				_, _ = c.Delete(k)
			}
		}(w)
	}
	wg.Wait()
}

func fmtKey(w, i int) string {
	return "k" + strconv.Itoa(w) + "_" + strconv.Itoa(i)
}

// heldSeqs returns distinct held sequences, ascending.
func (c *Compactor) heldSeqs() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []int64
	for s, n := range c.holds {
		if n > 0 {
			out = append(out, s)
		}
	}
	sortInt64(out)
	return out
}

func sortInt64(a []int64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
