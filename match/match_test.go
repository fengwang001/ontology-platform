package match

import (
	"math/rand"
	"testing"
)

// TestGreedyBacktrack pins pairs where a greedy '*' needs backtracking
// (invariant 2) and the trailing-star case (question 丙).
func TestGreedyBacktrack(t *testing.T) {
	cases := []struct {
		s, p string
		want bool
	}{
		{"adceb", "*a*b", true}, {"aab", "*ab", true}, {"ab", "a*", true},
		{"acdcb", "a*c?b", false}, {"abcab", "*ab", true}, {"aaaa", "***a", true},
		{"aa", "a", false}, {"cb", "?a", false}, {"abc", "*cc", false},
		{"", "*", true}, {"", "**", true}, {"a", "", false}, {"", "", true},
		{"mississippi", "m*issi*pi", true}, {"babaaabaa", "*ba*b*", true},
	}
	for _, c := range cases {
		if got := Match(c.s, c.p); got != c.want {
			t.Errorf("Match(%q,%q)=%v want %v", c.s, c.p, got, c.want)
		}
	}
}

// TestWildcardSemantics pins exact one-char '?' and empty/any '*' (inv. 3).
func TestWildcardSemantics(t *testing.T) {
	cases := []struct {
		s, p string
		want bool
	}{
		{"ab", "a?b", false}, {"acb", "a?b", true}, {"", "?", false},
		{"a", "?", true}, {"abc", "a??", true}, {"ab", "a??", false},
		{"ab", "*ab", true}, {"xaby", "*ab", false}, {"anything", "*", true},
		{"中界", "中?", true}, {"中界", "?界", true}, {"界", "中?", false},
	}
	for _, c := range cases {
		if got := Match(c.s, c.p); got != c.want {
			t.Errorf("Match(%q,%q)=%v want %v", c.s, c.p, got, c.want)
		}
	}
}

// TestNaiveEquivalence compares the two-pointer core against the naive DP
// on fixed pairs and on many random pairs, including multi-byte runes.
func TestNaiveEquivalence(t *testing.T) {
	fixed := []struct{ s, p string }{
		{"adceb", "*a*b"}, {"aab", "*ab"}, {"ab", "a?b"}, {"ab", "a*"},
		{"acdcb", "a*c?b"}, {"", "***"}, {"a中b", "*中*"}, {"中界", "中?"},
	}
	for _, c := range fixed {
		if Match(c.s, c.p) != naiveDP(c.s, c.p) {
			t.Errorf("divergence Match(%q,%q): core=%v dp=%v", c.s, c.p, Match(c.s, c.p), naiveDP(c.s, c.p))
		}
	}
	const alpha = "ab?*中"
	rng := rand.New(rand.NewSource(763))
	for iter := 0; iter < 4000; iter++ {
		s := randStr(rng, "ab中", rng.Intn(7))
		p := randStr(rng, alpha, rng.Intn(7))
		if got, dp := Match(s, p), naiveDP(s, p); got != dp {
			t.Fatalf("random divergence Match(%q,%q): core=%v dp=%v", s, p, got, dp)
		}
	}
}

func randStr(rng *rand.Rand, alpha string, n int) string {
	r := make([]rune, n)
	for i := range r {
		r[i] = []rune(alpha)[rng.Intn(len([]rune(alpha)))]
	}
	return string(r)
}

// TestComplexityLinear reads the unexported steps counter directly (the
// only allowed reader: same-package white-box) and shows the cost is a
// constant multiple of n+m across m = 100..10000, i.e. it does not grow
// with n*m; it is O(n+m), not quadratic or exponential.
func TestComplexityLinear(t *testing.T) {
	const n, C = 6, 16
	ms := []int{100, 500, 1000, 5000, 10000}
	ratios := make([]float64, len(ms))
	for k, mL := range ms {
		m := &matcher{pattern: []rune("*aaaab")}
		m.run([]rune(repeat('a', mL-1) + "b"))
		if m.steps > C*(n+mL) {
			t.Fatalf("m=%d steps=%d exceeds %d*(n+m)", mL, m.steps, C)
		}
		ratios[k] = float64(m.steps) / float64(n+mL)
	}
	// A quadratic/exponential core would inflate the ratio with m; the
	// linear ratio must stay essentially flat (small constant slack).
	if ratios[len(ratios)-1] > ratios[0]+2 {
		t.Fatalf("step ratio not flat: %v", ratios)
	}
}
