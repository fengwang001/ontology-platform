package ontology

import (
	"fmt"
	"testing"
)

// interleaveFixture builds the boundary-laden scenario used by the
// exhaustive tests: three timezone definition migrations with writes
// anchored to each version, all sitting on group boundaries.
//
// Anchoring timeline (engine serial order):
//
//	v1 = Asia/Shanghai (UTC+8):  a1, a2 written
//	migration to v2 = UTC:       a3 written
//	migration to v3 = New_York:  a4 written
//
// All wall clocks are chosen on UTC day boundaries so that any anchoring
// mistake flips an object into a different group.
func interleaveFixture(t *testing.T) (*Engine, []Event) {
	t.Helper()
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "Asia/Shanghai"))
	mustDo(t, e.DefineTimezone("B", "UTC"))

	wb, _ := e.Write("b1", "B", "tb", MustWall("2026-01-15T12:00:00"))

	a1, _ := e.Write("a1", "A", "ta", MustWall("2026-01-15T08:00:00")) // v1 -> 00:00:00 UTC
	a2, _ := e.Write("a2", "A", "ta", MustWall("2026-01-16T07:59:59")) // v1 -> 23:59:59 UTC (prev day boundary)
	mustDo(t, e.MigrateTimezone("A", "UTC", 1))
	a3, _ := e.Write("a3", "A", "ta", MustWall("2026-01-16T00:00:00")) // v2 -> 00:00:00 UTC
	mustDo(t, e.MigrateTimezone("A", "America/New_York", 2))
	a4, _ := e.Write("a4", "A", "ta", MustWall("2026-01-15T19:00:00")) // v3 -> 00:00:00 UTC next day

	var evs []Event
	evs = append(evs, wb)
	for i, w := range []Event{a1, a2, a3, a4} {
		id := fmt.Sprintf("a%d", i+1)
		l, err := e.Link("L", id, "A", "b1", "B")
		mustDo(t, err)
		evs = append(evs, w, l)
	}
	return e, evs
}

// canonicalResult is the unique correct outcome, independent of arrival
// order: a1/a2 keep v1 (+8), a3 uses v2 (UTC), a4 uses v3 (-5).
var canonicalResult = []string{
	"2026-01-15/a1", // 00:00:00
	"2026-01-15/b1", // 12:00:00
	"2026-01-15/a2", // 23:59:59
	"2026-01-16/a3", // 00:00:00, tie with a4 broken by object id
	"2026-01-16/a4", // 00:00:00
}

// TestExhaustiveArrivalPermutations: every one of the 8! permutations of the
// post-migration event stream must yield exactly the canonical result.
func TestExhaustiveArrivalPermutations(t *testing.T) {
	// events[0] is the b1 write, always delivered first; the remaining 8
	// (4 writes + 4 links across 3 timezone versions) are permuted.
	count := 0
	forEachPermutation(8, func(p []int) {
		count++
		e, evs := interleaveFixture(t)
		e.Dispatch(evs[0])
		for _, i := range p {
			e.Dispatch(evs[1+i])
		}
		groups, rep, err := e.Query("v")
		mustDo(t, err)
		if rep.Err != nil {
			t.Fatalf("perm %v: unexpected error %v", p, rep.Err)
		}
		if got := flatten(groups); !equalStrings(got, canonicalResult) {
			t.Fatalf("perm %v: got %v, want %v", p, got, canonicalResult)
		}
		// cross-check against the naive full-rebuild model
		if diff := CompareGroups(e.NaiveRebuild("L"), groups); diff != "" {
			t.Fatalf("perm %v: model mismatch: %s", p, diff)
		}
		checkViewInvariants(t, e, "v")
	})
	if count != 40320 {
		t.Fatalf("expected 40320 permutations, ran %d", count)
	}
}

// TestExhaustiveSyncPoints: for a fixed arrival order, every possible
// partitioning of the stream into sync batches (the view may have processed
// any prefix before the migration-era events arrive) must converge to the
// canonical result.
func TestExhaustiveSyncPoints(t *testing.T) {
	_, evs := interleaveFixture(t)
	n := len(evs)
	// 2^(n-1) ways to cut the stream into batches
	for mask := 0; mask < 1<<(n-1); mask++ {
		e, evs := interleaveFixture(t)
		var batch []Event
		flush := func() {
			if len(batch) > 0 {
				e.Dispatch(batch...)
				if _, err := e.Sync("v"); err != nil {
					t.Fatal(err)
				}
				batch = nil
			}
		}
		for i, ev := range evs {
			batch = append(batch, ev)
			if i < n-1 && mask&(1<<i) != 0 {
				flush()
			}
		}
		flush()
		groups, _, err := e.Query("v")
		mustDo(t, err)
		if got := flatten(groups); !equalStrings(got, canonicalResult) {
			t.Fatalf("sync mask %b: got %v, want %v", mask, got, canonicalResult)
		}
	}
}

// forEachPermutation calls f with every permutation of [0,n).
func forEachPermutation(n int, f func([]int)) {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			f(p)
			return
		}
		for i := k; i < n; i++ {
			p[k], p[i] = p[i], p[k]
			rec(k + 1)
			p[k], p[i] = p[i], p[k]
		}
	}
	rec(0)
}
