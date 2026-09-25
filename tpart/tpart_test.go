package tpart

import (
	"testing"

	"ontology/tbucket"
)

// TestFloorDiv pins floor division on negative timestamps.
func TestFloorDiv(t *testing.T) {
	cases := []struct{ a, b, want int64 }{
		{-12, 10, -2}, {-1, 10, -1}, {0, 10, 0}, {9, 10, 0}, {10, 10, 1},
		{-10, 10, -1}, {-11, 10, -2}, {7, 3, 2}, {-7, 3, -3}, {-100, 7, -15},
	}
	for _, c := range cases {
		if got := tbucket.FloorDiv(c.a, c.b); got != c.want {
			t.Fatalf("FloorDiv(%d,%d)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestCleanupCheckedBounded proves cleanup inspects only the buckets
// that actually expire: with R=m covering all m live buckets, one
// event advancing cur by 1 must inspect a constant number of buckets,
// independent of m (no whole-table scan).
func TestCleanupCheckedBounded(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} {
		p := New(1, m)
		for ts := int64(0); ts < m; ts++ { // fill buckets 0..m-1
			p.Add(ts, "k")
		}
		p.Add(m, "k") // advance cur by 1, expire exactly bucket 0
		if p.checked > 2 {
			t.Fatalf("m=%d: checked %d buckets, grows with m", m, p.checked)
		}
		if p.dropped != 1 || int64(len(p.counts)) != m {
			t.Fatalf("m=%d: dropped=%d counts=%d", m, p.dropped, len(p.counts))
		}
	}
}
