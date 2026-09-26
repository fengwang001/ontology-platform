package cyc

import (
	"math/rand/v2"
	"testing"
)

func naiveMinRotation(s string) int {
	best := 0
	for k := 1; k < len(s); k++ {
		if s[k:]+s[:k] < s[best:]+s[:best] {
			best = k
		}
	}
	return best
}

func TestMinRotationMatchesNaive(t *testing.T) {
	fixed := []string{"baabaa", "banana", "bca", "aaaa", "z", "ab", "ba", "abacaba"}
	for _, s := range fixed {
		if got := MinRotation(s); got != naiveMinRotation(s) {
			t.Errorf("MinRotation(%q)=%d, naive=%d", s, got, naiveMinRotation(s))
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for n := 1; n <= 60; n++ {
		for trial := 0; trial < 20; trial++ {
			b := make([]byte, n)
			for i := range b {
				b[i] = byte('a' + rng.IntN(4)) // small alphabet forces ties
			}
			s := string(b)
			if got := MinRotation(s); got != naiveMinRotation(s) {
				t.Fatalf("MinRotation(%q)=%d, naive=%d", s, got, naiveMinRotation(s))
			}
		}
	}
}

func TestRotateConsistent(t *testing.T) {
	cases := []struct {
		s   string
		k   int
		rot string
	}{
		{"baabaa", 1, "aabaab"},
		{"banana", 5, "abanan"},
		{"bca", 2, "abc"},
		{"aaaa", 0, "aaaa"},
	}
	for _, c := range cases {
		if got := Rotate(c.s, c.k); got != c.rot || got != c.s[c.k:]+c.s[:c.k] {
			t.Errorf("Rotate(%q,%d)=%q, want %q", c.s, c.k, got, c.rot)
		}
		k := MinRotation(c.s)
		if Rotate(c.s, k) != c.s[k:]+c.s[:k] {
			t.Errorf("Rotate(MinRotation(%q)) inconsistent", c.s)
		}
	}
}

// TestComparisonCountIsLinear proves Booth is O(n): the unexported
// counter cmpCount is read here (same package), never via the API.
func TestComparisonCountIsLinear(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + rng.IntN(26))
		}
		MinRotation(string(b))
		if got := cmpCount.Load(); got > int64(3*n) {
			t.Errorf("n=%d: comparisons=%d > 3n=%d (grows like n^2?)", n, got, 3*n)
		}
	}
}
