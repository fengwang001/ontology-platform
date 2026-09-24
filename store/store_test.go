package store

import (
	"fmt"
	"testing"

	"ontology/tier"
)

// TestEvictionScanBounded proves victim selection is O(1): after filling
// memory at several capacities, the single triggering eviction inspects a
// constant number of entries regardless of m. The unexported counter is read
// directly here (never through an exported function or method).
func TestEvictionScanBounded(t *testing.T) {
	cases := []int{100, 1000, 10000}
	var prev int
	for _, m := range cases {
		s := New(m)
		for i := 0; i < m; i++ {
			s.Write(fmt.Sprintf("k%05d", i), "v")
		}
		s.Write("extra", "v") // forces exactly one eviction
		if got := s.lastEvictScans; got != 1 || got > evictionScanBound {
			t.Fatalf("m=%d: eviction inspected %d entries, want constant 1", m, got)
		}
		if len(s.HotKeys()) != m {
			t.Fatalf("m=%d: hot size %d, want %d", m, len(s.HotKeys()), m)
		}
		if m > 100 && s.lastEvictScans != prev {
			t.Fatalf("scan count grew with m: %d vs %d", s.lastEvictScans, prev)
		}
		prev = s.lastEvictScans
	}
}

// TestTierOrdering pins timestamp ordering, the lexicographic tie-break and
// O(1) victim/touch/remove behaviour of the tier index.
func TestTierOrdering(t *testing.T) {
	cases := []struct {
		a, b tier.Entry
		want bool
	}{
		{tier.Entry{Key: "a", Stamp: 1}, tier.Entry{Key: "b", Stamp: 2}, true},
		{tier.Entry{Key: "b", Stamp: 2}, tier.Entry{Key: "a", Stamp: 1}, false},
		{tier.Entry{Key: "a", Stamp: 1}, tier.Entry{Key: "b", Stamp: 1}, true},
		{tier.Entry{Key: "b", Stamp: 1}, tier.Entry{Key: "a", Stamp: 1}, false},
	}
	for _, c := range cases {
		if got := tier.Older(c.a, c.b); got != c.want {
			t.Fatalf("Older(%v,%v)=%v want %v", c.a, c.b, got, c.want)
		}
	}
	x := tier.NewIndex()
	x.Add(tier.Entry{Key: "a", Stamp: 1})
	x.Add(tier.Entry{Key: "b", Stamp: 1})
	if v, _ := x.Victim(); v.Key != "a" { // equal stamp -> smaller key
		t.Fatalf("victim=%q want a", v.Key)
	}
	if !x.Touch("a", 2) {
		t.Fatal("Touch missing key reported false")
	}
	if v, _ := x.Victim(); v.Key != "b" {
		t.Fatalf("victim after touch=%q want b", v.Key)
	}
	if e, ok := x.Remove("b"); !ok || e.Key != "b" || x.Len() != 1 {
		t.Fatal("Remove did not drop b")
	}
	if _, ok := x.Victim(); !ok {
		t.Fatal("remaining victim should exist")
	}
}

// TestStorePromoteEvict checks transparent promotion, disk-read accounting and
// authoritative cold values at the store layer.
func TestStorePromoteEvict(t *testing.T) {
	s := New(2)
	steps := []struct {
		w          bool
		k, v       string
		wantVal    string
		wantFound  bool
		wantReads  int
		wantColdOK bool
	}{
		{true, "A", "1", "", false, 0, true},
		{true, "B", "2", "", false, 0, true},
		{false, "A", "", "1", true, 0, true},
		{true, "C", "3", "", false, 0, true}, // evicts B
		{false, "B", "", "2", true, 1, true}, // B served from disk, promoted
		{false, "D", "", "", false, 2, false},
	}
	for i, st := range steps {
		if st.w {
			s.Write(st.k, st.v)
			if cv, ok := s.ColdValue(st.k); !ok || cv != st.v {
				t.Fatalf("step %d: cold %q=%q,%v want %q", i+1, st.k, cv, ok, st.v)
			}
		} else {
			v, f := s.Read(st.k)
			if v != st.wantVal || f != st.wantFound || s.DiskReads() != st.wantReads {
				t.Fatalf("step %d Read(%s)=%q,%v reads=%d want %q,%v reads=%d",
					i+1, st.k, v, f, s.DiskReads(), st.wantVal, st.wantFound, st.wantReads)
			}
		}
	}
}
