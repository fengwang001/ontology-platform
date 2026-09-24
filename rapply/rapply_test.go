package rapply

import (
	"sync"
	"sync/atomic"
	"testing"

	"ontology/rimg"
)

// TestReadCountBounded proves judgement reads O(1) rows regardless of table
// size m: the unexported counter stays within a small constant for a matching
// Update, a mismatching Delete and an existing-PK Insert.
func TestReadCountBounded(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		init := make(map[int64]rimg.Row, m)
		for i := range m {
			init[int64(i+1)] = rimg.Row{"c": "v"}
		}
		r := New(init, m+10)
		cases := []rimg.Event{
			{Seq: 1, Kind: rimg.Update, PK: 1, Before: rimg.Row{"c": "v"}, After: rimg.Row{"c": "w"}},
			{Seq: 2, Kind: rimg.Delete, PK: 2, Before: rimg.Row{"c": "DIFF"}},
			{Seq: 3, Kind: rimg.Insert, PK: 1, After: rimg.Row{"c": "x"}},
		}
		for _, e := range cases {
			if _, err := r.Apply([]rimg.Event{e}); err != nil {
				t.Fatalf("m=%d seq=%d: %v", m, e.Seq, err)
			}
			if r.reads > 2 {
				t.Fatalf("m=%d seq=%d: reads=%d grows with table size", m, e.Seq, r.reads)
			}
		}
	}
}

// TestConcurrentBoundary: one writer commits batches of K inserts; every
// State() read by concurrent readers must be an exact committed boundary
// (no sleeps; goroutines synchronized via atomics and WaitGroup).
func TestConcurrentBoundary(t *testing.T) {
	const B, K = 50, 5
	r := New(map[int64]rimg.Row{}, B*K)
	var fail, stop atomic.Bool
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				rows, cf, s := r.State()
				if s%K != 0 || int64(len(rows)) != s || len(cf) != 0 {
					fail.Store(true)
				}
			}
		}()
	}
	for b := 0; b < B; b++ {
		evs := make([]rimg.Event, K)
		for k := range K {
			n := int64(b*K + k + 1)
			evs[k] = rimg.Event{Seq: n, Kind: rimg.Insert, PK: n, After: rimg.Row{"v": "x"}}
		}
		if _, err := r.Apply(evs); err != nil {
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if fail.Load() {
		t.Fatal("reader observed a non-boundary state")
	}
}

// TestRowEqual pins full-row equality (missing column != empty string) and
// event-shape validation, table driven.
func TestRowEqual(t *testing.T) {
	row := func(s ...string) rimg.Row {
		r := rimg.Row{}
		for i := 0; i+1 < len(s); i += 2 {
			r[s[i]] = s[i+1]
		}
		return r
	}
	eqCases := []struct {
		a, b  rimg.Row
		equal bool
	}{
		{row("a", "1"), row("a", "1"), true},
		{row("a", "1", "q", ""), row("a", "1"), false}, // empty string present
		{row("a", "1"), row("a", "1", "q", ""), false}, // vs column missing
		{row("a", "1"), row("a", "2"), false},
		{row("a", "1", "b", "2"), row("b", "2", "a", "1"), true},
	}
	for i, c := range eqCases {
		if rimg.RowEqual(c.a, c.b) != c.equal {
			t.Fatalf("equal case %d", i)
		}
	}
	bad := []rimg.Event{
		{Seq: 1, Kind: rimg.Insert, After: nil},                                  // missing After
		{Seq: 1, Kind: rimg.Insert, Before: row("a", "1"), After: row("b", "2")}, // Before present
		{Seq: 1, Kind: rimg.Update, Before: row("a", "1")},                       // missing After
		{Seq: 1, Kind: rimg.Delete, Before: row()},                               // empty map image
		{Seq: 1, Kind: rimg.Delete, Before: row("a", "1"), After: row("b", "2")}, // After present
		{Seq: 1, Kind: rimg.Insert, After: row("", "v")},                         // empty column name
		{Seq: 1, Kind: rimg.Kind(9), After: row("a", "1")},                       // unknown kind
	}
	for i, e := range bad {
		if rimg.ValidEvent(e) == nil {
			t.Fatalf("bad event %d accepted", i)
		}
	}
	good := []rimg.Event{
		{Seq: 1, Kind: rimg.Insert, After: row("a", "")},
		{Seq: 2, Kind: rimg.Update, Before: row("a", "1"), After: row("a", "2")},
		{Seq: 3, Kind: rimg.Delete, Before: row("a", "1")},
	}
	for i, e := range good {
		if rimg.ValidEvent(e) != nil {
			t.Fatalf("good event %d rejected", i)
		}
	}
}
