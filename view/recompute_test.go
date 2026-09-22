package view_test

import (
	"fmt"
	"testing"

	"ontology/agg"
	"ontology/change"
	"ontology/view"
)

func newTestView(t *testing.T) *view.View {
	t.Helper()
	v, err := view.New(view.Options{JournalPath: ""})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return v
}

func ins(ver uint64, key, group string, val float64) change.Change {
	return change.Change{Version: ver, Op: change.OpInsert, Key: key,
		From: change.Row{Group: group, GroupPresent: true, Value: val}}
}

func del(ver uint64, key, group string, val float64) change.Change {
	return change.Change{Version: ver, Op: change.OpDelete, Key: key,
		From: change.Row{Group: group, GroupPresent: true, Value: val}}
}

// TestRecomputeCountExactlyThree constructs 100k records and 5000
// deletions, exactly three of which remove the current group minimum.
// Min must recompute three times; Count and Sum zero times.
func TestRecomputeCountExactlyThree(t *testing.T) {
	if testing.Short() {
		t.Skip("100k-record scenario")
	}
	v := newTestView(t)
	const n = 100_000
	ver := uint64(0)
	next := func() uint64 { ver++; return ver }

	for i := 0; i < n; i++ {
		if err := v.Submit(ins(next(), fmt.Sprintf("k%06d", i), "g", float64(i+1))); err != nil {
			t.Fatal(err)
		}
	}

	// Delete 3 unique minima first (values 1, 2, 3), each forcing one Min
	// recomputation; then delete 4997 middle members that are never the
	// current minimum or maximum.
	for _, i := range []int{0, 1, 2} {
		if err := v.Submit(del(next(), fmt.Sprintf("k%06d", i), "g", float64(i+1))); err != nil {
			t.Fatal(err)
		}
	}
	done := 3
	const lowestMid = 1000
	for i := lowestMid; done < 5000; i++ {
		if err := v.Submit(del(next(), fmt.Sprintf("k%06d", i), "g", float64(i+1))); err != nil {
			t.Fatal(err)
		}
		done++
	}

	s := v.Stats()
	if s.TriggersByKind[agg.Min] != 3 {
		t.Fatalf("Min recomputes = %d, want 3", s.TriggersByKind[agg.Min])
	}
	if s.TriggersByKind[agg.Count] != 0 {
		t.Fatalf("Count recomputes = %d, want 0", s.TriggersByKind[agg.Count])
	}
	if s.TriggersByKind[agg.Sum] != 0 {
		t.Fatalf("Sum recomputes = %d, want 0", s.TriggersByKind[agg.Sum])
	}

	g, ok := v.Lookup("g")
	if !ok {
		t.Fatal("group disappeared")
	}
	if g.Count != n-5000 {
		t.Fatalf("count=%v want %d", g.Count, n-5000)
	}
	if g.Min != 4 {
		t.Fatalf("min=%v want 4", g.Min)
	}
	if g.Max != n {
		t.Fatalf("max=%v want %d", g.Max, n)
	}
}

// TestRecomputeVisitsOnlyOwnGroup bounds member access and proves other
// groups are never scanned.
func TestRecomputeVisitsOnlyOwnGroup(t *testing.T) {
	v := newTestView(t)
	changes := []change.Change{
		ins(1, "a1", "a", 1), ins(2, "a2", "a", 2), ins(3, "a3", "a", 3),
		ins(4, "b1", "b", 10), ins(5, "b2", "b", 20),
	}
	for _, c := range changes {
		if err := v.Submit(c); err != nil {
			t.Fatal(err)
		}
	}
	before := v.Stats().MembersByKind[agg.Min]
	if err := v.Submit(del(6, "a1", "a", 1)); err != nil {
		t.Fatal(err)
	}
	visited := v.Stats().MembersByKind[agg.Min] - before
	// Group a has exactly 2 surviving members; b must not be touched.
	if visited != 2 {
		t.Fatalf("visited %d members, want 2 (own group only)", visited)
	}
}
