package tkn

import "testing"

func TestNewBucketStartsFull(t *testing.T) {
	b := New(20, 2)
	if got := b.Tokens(); got != 20 {
		t.Fatalf("initial tokens = %d, want 20", got)
	}
	if b.Capacity() != 20 || b.Rate() != 2 {
		t.Fatalf("capacity/rate = %d/%d, want 20/2", b.Capacity(), b.Rate())
	}
}

func TestRefillAndConsume(t *testing.T) {
	cases := []struct {
		name             string
		cap, rate        int64
		preConsume       int64
		elapsed          int64
		wantAfterRefill  int64
		need             int64
		wantAllowed      bool
		wantAfterConsume int64
	}{
		{"partial", 20, 2, 15, 3, 11, 10, true, 1},
		{"capped-at-cap", 20, 2, 20, 100, 20, 1, true, 19},
		{"rejected-no-change", 5, 1, 0, 0, 5, 6, false, 5},
		{"zero-elapsed-noop", 7, 3, 0, 0, 7, 7, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := New(tc.cap, tc.rate)
			if tc.preConsume > 0 {
				b.TryConsume(tc.preConsume) // set the starting token count
			}
			b.Refill(tc.elapsed)
			if b.Tokens() != tc.wantAfterRefill {
				t.Fatalf("after refill tokens=%d, want %d", b.Tokens(), tc.wantAfterRefill)
			}
			if got := b.TryConsume(tc.need); got != tc.wantAllowed {
				t.Fatalf("TryConsume(%d)=%v, want %v", tc.need, got, tc.wantAllowed)
			}
			if b.Tokens() != tc.wantAfterConsume {
				t.Fatalf("after consume tokens=%d, want %d", b.Tokens(), tc.wantAfterConsume)
			}
		})
	}
}

// TestTokensBounds pins invariant 2 over many random-ish refill/consume
// loops: tokens must always stay within [0, capacity].
func TestTokensBounds(t *testing.T) {
	caps := []int64{1, 7, 20, 100, 999}
	for _, cap := range caps {
		b := New(cap, 3)
		for step := int64(0); step < 500; step++ {
			b.Refill(step % 13) // refill before consuming
			need := (step*7)%(cap+2) + 1
			b.TryConsume(need)
			got := b.Tokens()
			if got < 0 || got > cap {
				t.Fatalf("cap=%d step=%d: tokens=%d out of [0,%d]", cap, step, got, cap)
			}
		}
	}
}

// TestRefillIsO1NotLinearInM proves refill is a single multiplication:
// for refill totals m from 100 to 10000 (rate=1, elapsed=m), the internal
// per-token increment counter must stay exactly 0 instead of growing with m.
// The unexported field is read directly here, never via an exported accessor.
func TestRefillIsO1NotLinearInM(t *testing.T) {
	for _, m := range []int64{100, 500, 1000, 2500, 5000, 10000} {
		b := New(m*2, 1) // capacity above m so the cap does not hide growth
		b.TryConsume(m * 2)
		if b.refillSteps != 0 {
			t.Fatalf("fresh bucket steps=%d, want 0", b.refillSteps)
		}
		b.Refill(m) // elapsed*rate == m, need afterwards is tiny
		if b.refillSteps != 0 {
			t.Fatalf("m=%d: refillSteps=%d, want 0 (O(1), no per-token loop)", m, b.refillSteps)
		}
		if !b.RefillIsO1() {
			t.Fatalf("m=%d: RefillIsO1()=false", m)
		}
		if b.TryConsume(1) != true || b.Tokens() != m-1 {
			t.Fatalf("m=%d: post-refill consume wrong, tokens=%d", m, b.Tokens())
		}
	}
}
