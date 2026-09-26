package pfx

import (
	"math/rand"
	"testing"
)

// naivePi recomputes π by brute force: for each prefix, try every proper
// border length from longest to shortest.
func naivePi(s string) []int {
	n := len(s)
	pi := make([]int, n)
	for i := 0; i < n; i++ {
		for b := i; b > 0; b-- {
			if s[:b] == s[i+1-b:i+1] {
				pi[i] = b
				break
			}
		}
	}
	return pi
}

func TestBuildMatchesNaive(t *testing.T) {
	cases := []string{
		"a", "ab", "aa", "abababa", "abcde", "aaaa",
		"abcabcabc", "aabaaab", "ababab", "xyzxyzx",
	}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 50; i++ {
		b := make([]byte, 1+rng.Intn(30))
		for j := range b {
			b[j] = byte('a' + rng.Intn(3))
		}
		cases = append(cases, string(b))
	}
	for _, s := range cases {
		pf, want := Build(s), naivePi(s)
		if pf.Len() != len(want) {
			t.Fatalf("%q: Len=%d want %d", s, pf.Len(), len(want))
		}
		for i := range want {
			if pf.At(i) != want[i] {
				t.Fatalf("%q: pi[%d]=%d want %d", s, i, pf.At(i), want[i])
			}
		}
	}
}

// TestLinearComparisons proves building π is O(n): the unexported comparison
// counter must stay within 2n for every scale and shape, never grow like n².
func TestLinearComparisons(t *testing.T) {
	sizes := []int{100, 500, 1000, 5000, 10000}
	rng := rand.New(rand.NewSource(3))
	gen := map[string]func(int) string{
		"rand2": func(n int) string {
			b := make([]byte, n)
			for i := range b {
				b[i] = byte('a' + rng.Intn(2))
			}
			return string(b)
		},
		"rand26": func(n int) string {
			b := make([]byte, n)
			for i := range b {
				b[i] = byte('a' + rng.Intn(26))
			}
			return string(b)
		},
		"repeat": func(n int) string {
			b := make([]byte, n)
			for i := range b {
				b[i] = byte('a' + i%2)
			}
			return string(b)
		},
		"same": func(n int) string {
			b := make([]byte, n)
			for i := range b {
				b[i] = 'a'
			}
			return string(b)
		},
	}
	for name, g := range gen {
		for _, n := range sizes {
			if pf := Build(g(n)); pf.cmp > 2*n {
				t.Fatalf("%s n=%d: cmp=%d exceeds 2n=%d (quadratic?)", name, n, pf.cmp, 2*n)
			}
		}
	}
}
