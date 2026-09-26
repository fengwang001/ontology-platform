package pfx

import (
	"math/rand"
	"testing"
)

func TestPrefixFunctionKnown(t *testing.T) {
	cases := []struct {
		s  string
		pi []int
	}{
		{"", []int{}},
		{"a", []int{0}},
		{"aaa", []int{0, 1, 2}},
		{"abababa", []int{0, 0, 1, 2, 3, 4, 5}},
		{"abcabcd", []int{0, 0, 0, 1, 2, 3, 0}},
		{"abcabcabc", []int{0, 0, 0, 1, 2, 3, 4, 5, 6}},
	}
	for _, c := range cases {
		got := Build(c.s)
		if got.Len() != len(c.s) {
			t.Errorf("Build(%q).Len = %d, want %d", c.s, got.Len(), len(c.s))
		}
		for i, want := range c.pi {
			if got.PiAt(i) != want {
				t.Errorf("Build(%q).π[%d] = %d, want %d (full π=%v)", c.s, i, got.PiAt(i), want, got.pi)
			}
		}
	}
}

// naivePi recomputes π by brute force and must agree with Build.
func naivePi(s string) []int {
	pi := make([]int, len(s))
	for i := 1; i < len(s); i++ {
		for l := i; l > 0; l-- {
			if s[:l] == s[i-l+1:i+1] {
				pi[i] = l
				break
			}
		}
	}
	return pi
}

func TestPrefixFunctionMatchesNaive(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for iter := 0; iter < 200; iter++ {
		n := 1 + r.Intn(40)
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + r.Intn(3))
		}
		s := string(b)
		got := Build(s)
		want := naivePi(s)
		for i := range want {
			if got.PiAt(i) != want[i] {
				t.Fatalf("Build(%q).π[%d] = %d, naive wants %d", s, i, got.PiAt(i), want[i])
			}
		}
	}
}

// TestComparisonCountLinear pins the real comparison count to O(n): across
// several sizes from 100 to 10000 and several random inputs per size it must
// never exceed 2n. An O(n²) rescan-per-prefix implementation would blow this.
func TestComparisonCountLinear(t *testing.T) {
	sizes := []int{100, 300, 1000, 3000, 10000}
	for _, n := range sizes {
		for seed := int64(0); seed < 5; seed++ {
			r := rand.New(rand.NewSource(seed + int64(n)))
			b := make([]byte, n)
			for i := range b {
				b[i] = byte('a' + r.Intn(4))
			}
			tab := Build(string(b))
			if tab.cmp > 2*n {
				t.Errorf("n=%d seed=%d: %d comparisons > 2n=%d (not linear)", n, seed, tab.cmp, 2*n)
			}
			if tab.cmp < n-1 {
				t.Errorf("n=%d seed=%d: only %d comparisons, counter looks broken", n, seed, tab.cmp)
			}
		}
	}
}
