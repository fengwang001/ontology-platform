package window

import (
	"math/big"
	"testing"
)

func TestEstimateFloorAndBoundary(t *testing.T) {
	// Window 0 saw 8 hits; rolling into window 1 keeps prev=8, cur=0.
	c := NewCounter(0, 100)
	c.cur = 8
	cases := []struct {
		now  int64
		want int64
	}{
		{100, 8}, // now%W == 0: previous window counts in full
		{150, 4}, // floor(8*50/100)
		{199, 0}, // floor(8*1/100) rounds down to 0
	}
	for _, tc := range cases {
		if got := c.Estimate(tc.now, 100); got != tc.want {
			t.Errorf("Estimate(%d) = %d, want %d", tc.now, got, tc.want)
		}
	}
}

func TestRollSameNextAndSkippedWindow(t *testing.T) {
	c := NewCounter(100, 100) // k=1
	c.cur = 6
	c.prev = 2

	// k' == k: nothing changes.
	if got := c.Estimate(150, 100); got != 6+1 { // 6 + floor(2*50/100)
		t.Fatalf("same-window est = %d, want 7", got)
	}

	// k' == k+1: prev <- cur, cur <- 0.
	d := c
	d.Advance(200, 100)
	if d.cur != 0 || d.prev != 6 || d.k != 2 {
		t.Fatalf("k+1 roll = %+v, want cur=0 prev=6 k=2", d)
	}
	if got := d.Estimate(200, 100); got != 6 {
		t.Fatalf("est at window start = %d, want 6", got)
	}

	// k' >= k+2: both windows dropped.
	e := c
	e.Advance(300, 100)
	if e.cur != 0 || e.prev != 0 || e.k != 3 {
		t.Fatalf("k+2 roll = %+v, want cur=0 prev=0 k=3", e)
	}
	if got := e.Estimate(350, 100); got != 0 {
		t.Fatalf("est after skip = %d, want 0", got)
	}
}

func TestIncCap(t *testing.T) {
	c := NewCounter(0, 100)
	for i := 0; i < 5; i++ {
		c.Inc(3)
	}
	if c.cur != 3 {
		t.Fatalf("cur = %d, want capped 3", c.cur)
	}
}

func TestEstimateNoOverflow(t *testing.T) {
	// Worst case from the spec: prev = 1e9+1, W = 1e9.
	c := Counter{k: 0, cur: 1_000_000_001, prev: 1_000_000_001}
	now, width := int64(999_999_999), int64(1_000_000_000)
	got := c.Estimate(now, width)

	// Independent big.Int reference.
	bigPrev := big.NewInt(c.prev)
	bigW := big.NewInt(width)
	num := new(big.Int).Mul(bigPrev, big.NewInt(width-now%width))
	want := new(big.Int).Add(new(big.Int).Quo(num, bigW), big.NewInt(c.cur))
	if got != want.Int64() {
		t.Fatalf("Estimate = %d, big.Int reference = %d", got, want.Int64())
	}
}
