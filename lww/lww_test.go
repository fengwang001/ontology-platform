package lww

import (
	"fmt"
	"maps"
	"testing"
)

// fullMerge is the naive reference: elementwise max of both record sets.
func fullMerge(dst, src map[string]Record) map[string]Record {
	out := maps.Clone(dst)
	if out == nil {
		out = map[string]Record{}
	}
	for e, sr := range src {
		r := out[e]
		r.A, r.R = max(r.A, sr.A), max(r.R, sr.R)
		out[e] = r
	}
	return out
}

func TestMergeLaws(t *testing.T) {
	mk := func(seed int64) *State { // identical replays per seed
		s := New(100)
		for i := int64(0); i < 20; i++ {
			e, ts := string(rune('a'+(seed+i*7)%5)), (seed*31+i*13)%40+1
			if (seed+i)%2 == 0 {
				s.Add(e, ts)
			} else {
				s.Remove(e, ts)
			}
		}
		return s
	}
	for _, seeds := range [][3]int64{{1, 2, 3}, {7, 7, 9}, {4, 5, 4}} {
		x1, x2, y1, y2 := mk(seeds[0]), mk(seeds[0]), mk(seeds[1]), mk(seeds[1])
		Merge(x1, y1)
		Merge(y2, x2)
		if !maps.Equal(x1.Snapshot(), y2.Snapshot()) {
			t.Fatalf("commutativity: %v", seeds)
		}
		a1, a2 := mk(seeds[0]), mk(seeds[0])
		b1, b2 := mk(seeds[1]), mk(seeds[1])
		c1, c2 := mk(seeds[2]), mk(seeds[2])
		Merge(a1, b1)
		Merge(a1, c1)
		Merge(b2, c2)
		Merge(a2, b2)
		if !maps.Equal(a1.Snapshot(), a2.Snapshot()) {
			t.Fatalf("associativity: %v", seeds)
		}
		before := x1.Snapshot()
		Merge(x1, y1) // (x⊔y)⊔y = x⊔y
		Merge(x1, x1) // x⊔x = x
		if !maps.Equal(before, x1.Snapshot()) {
			t.Fatalf("idempotency: %v", seeds)
		}
	}
}

func TestIncrementalChecked(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		src, dst := New(m+10), New(m+10)
		for i := 0; i < m; i++ {
			src.Add(fmt.Sprintf("e%d", i), int64(i+1))
		}
		if err := Merge(dst, src); err != nil {
			t.Fatal(err)
		}
		if dst.checked != m {
			t.Fatalf("m=%d: first merge checked %d, want %d", m, dst.checked, m)
		}
		src.Add("zz", 1) // exactly one change since dst's last merge
		if err := Merge(dst, src); err != nil {
			t.Fatal(err)
		}
		if dst.checked > 1+3 { // changes + small constant, must not grow with m
			t.Fatalf("m=%d: incremental merge checked %d elements", m, dst.checked)
		}
		if got, want := dst.Snapshot(), fullMerge(nil, src.Snapshot()); !maps.Equal(got, want) {
			t.Fatalf("m=%d: incremental result differs from full merge", m)
		}
	}
}
