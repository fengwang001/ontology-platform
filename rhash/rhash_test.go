package rhash

import (
	"math/rand"
	"testing"
)

func naiveHash(s []byte, l, r int) (uint64, uint64) {
	h1, h2 := uint64(0), uint64(0)
	for _, c := range s[l:r] {
		h1 = (h1*B + uint64(c)) % M1
		h2 = (h2*B + uint64(c)) % M2
	}
	return h1, h2
}

// Invariant 2: Hash(l,r) must equal a byte-by-byte recompute of s[l:r).
func TestHashMatchesRecompute(t *testing.T) {
	for _, n := range []int{1, 2, 3, 100, 999, 10000} {
		rng := rand.New(rand.NewSource(int64(n)))
		s := make([]byte, n)
		for i := range s {
			s[i] = byte(rng.Intn(256))
		}
		tb := New(s)
		for k := 0; k < 300; k++ {
			l := rng.Intn(n + 1)
			r := l + rng.Intn(n-l+1)
			g1, g2 := tb.Hash(l, r)
			w1, w2 := naiveHash(s, l, r)
			if g1 != w1 || g2 != w2 {
				t.Fatalf("n=%d [%d,%d): got (%d,%d), want (%d,%d)", n, l, r, g1, g2, w1, w2)
			}
		}
	}
}

// Complexity: substring hashes come from the prefix tables in O(1), so
// the number of re-scanned characters must stay 0 for any query mix.
func TestNoRescan(t *testing.T) {
	for _, n := range []int{100, 1000, 5000, 10000} {
		rng := rand.New(rand.NewSource(int64(n)))
		s := make([]byte, n)
		for i := range s {
			s[i] = byte(rng.Intn(256))
		}
		tb := New(s)
		for m := 0; m < 500; m++ {
			l1 := rng.Intn(n + 1)
			r1 := l1 + rng.Intn(n-l1+1)
			l2 := rng.Intn(n + 1)
			r2 := l2 + rng.Intn(n-l2+1)
			if r1-l1 == r2-l2 {
				a1, a2 := tb.Hash(l1, r1)
				b1, b2 := tb.Hash(l2, r2)
				_ = a1 == b1 && a2 == b2
			}
		}
		if tb.rescanned != 0 {
			t.Fatalf("n=%d: rescanned=%d, want 0", n, tb.rescanned)
		}
	}
}
