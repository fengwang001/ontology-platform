package chain

import (
	"testing"

	"ontology/ver"
)

// TestAsOfComparisonCount proves AsOf binary-searches the ts-ordered
// chain: the number of compared versions stays bounded by a small
// constant and does not grow linearly with the chain length m.
// lastCmp is read directly here (same package); it is never exported.
func TestAsOfComparisonCount(t *testing.T) {
	const limit = 20 // > ceil(log2(10000)) = 14, independent of m
	for _, m := range []int64{100, 1000, 10000} {
		c := New()
		for ts := int64(1); ts <= m; ts++ {
			c.Insert(ver.Value(ts, "v"))
		}
		for _, T := range []int64{1, m / 2, m - 1, m, m + 5} {
			v, ok := c.AsOf(T)
			if c.lastCmp > limit {
				t.Errorf("m=%d T=%d: compared %d versions, want <= %d",
					m, T, c.lastCmp, limit)
			}
			want := T
			if want > m {
				want = m
			}
			if !ok || v.TS() != want {
				t.Errorf("m=%d T=%d: got ts=%d ok=%v, want ts=%d",
					m, T, v.TS(), ok, want)
			}
		}
	}
}

// TestInsertKeepsOrder checks out-of-order inserts stay ts-ascending.
func TestInsertKeepsOrder(t *testing.T) {
	c := New()
	for _, ts := range []int64{30, 10, 20, 5, 25} {
		c.Insert(ver.Value(ts, "x"))
	}
	vs := c.Versions()
	for i := 1; i < len(vs); i++ {
		if !vs[i-1].Before(vs[i]) {
			t.Fatalf("chain not ascending at index %d", i)
		}
	}
}
