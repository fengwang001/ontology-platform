package match

import (
	"math/rand"
	"strings"
	"testing"
)

func TestWildcardSemantics(t *testing.T) {
	cases := []struct {
		s, p string
		want bool
	}{
		{"", "", true}, {"", "*", true}, {"", "**", true}, {"", "?", false},
		{"", "a", false}, {"a", "?", true}, {"ab", "??", true}, {"", "a?", false},
		{"abc", "a?c", true}, {"ac", "a?c", false}, {"a", "a*", true},
		{"abcdef", "*", true}, {"", "***", true}, {"ab", "a*", true},
		{"adceb", "*a*b", true}, {"aab", "*ab", true}, {"ab", "a?b", false},
		{"acdcb", "a*c?b", false}, {"aa", "*a", true}, {"a", "*a", true},
		{"ab", "a?b", false}, {"漢字", "漢?", true}, {"世", "?", true},
	}
	for _, c := range cases {
		if got := Match(c.s, c.p); got != c.want {
			t.Errorf("Match(%q,%q)=%v want %v", c.s, c.p, got, c.want)
		}
	}
}

func TestBacktrackingFindsMatch(t *testing.T) {
	// Cases a greedy, non-backtracking '*' would get wrong.
	cases := []struct {
		s, p string
		want bool
	}{
		{"aab", "*ab", true},
		{"aaabab", "*ab", true},
		{"ab", "a*", true},       // trailing '*' must swallow the rest
		{"xxxxb", "*a*b", false}, // backtracking must also keep a true negative
		{"mississippi", "*m*i", true},
		{"aaaa", "a*a", true},
		{"hoxhos", "ho?ho*s", true},
	}
	for _, c := range cases {
		if got := Match(c.s, c.p); got != c.want {
			t.Errorf("Match(%q,%q)=%v want %v", c.s, c.p, got, c.want)
		}
	}
}

func TestMatchEqualsNaiveDP(t *testing.T) {
	cases := []struct{ s, p string }{
		{"adceb", "*a*b"}, {"aab", "*ab"}, {"ab", "a?b"}, {"ab", "a*"},
		{"acdcb", "a*c?b"}, {"", "***"}, {"abc", "???"}, {"mississippi", "m*i*p*i"},
		{"aaabbb", "*a?b*"}, {"", ""}, {"a", ""},
	}
	for _, c := range cases {
		if !AgreesWithNaive(c.s, c.p) {
			t.Errorf("disagree on (%q,%q): greedy=%v naive=%v",
				c.s, c.p, Match(c.s, c.p), naiveDP(c.s, c.p))
		}
	}
	// Random pairs: loops generate every case, no fixtures beyond the table.
	rng := rand.New(rand.NewSource(1))
	alpha := []rune{'a', 'b', '?', '*'}
	for iter := 0; iter < 4000; iter++ {
		lp := rng.Intn(8)
		ls := rng.Intn(10)
		pb := make([]rune, lp)
		for i := range pb {
			pb[i] = alpha[rng.Intn(len(alpha))]
		}
		sb := make([]rune, ls)
		for i := range sb {
			sb[i] = []rune("ab")[rng.Intn(2)]
		}
		s, p := string(sb), string(pb)
		if Match(s, p) != naiveDP(s, p) {
			t.Fatalf("random disagree (%q,%q): greedy=%v naive=%v", s, p, Match(s, p), naiveDP(s, p))
		}
	}
}

func TestAdvanceCountIsLinear(t *testing.T) {
	const pattern = "*a*a?b" // fixed n = 6
	q, err := Compile(pattern)
	if err != nil {
		t.Fatal(err)
	}
	n := len(q.r)
	prev := 0
	for _, m := range []int{100, 316, 1000, 3162, 10000} {
		// All-a text forces repeated backtracking yet can never match
		// (the pattern requires a trailing 'b').
		s := strings.Repeat("a", m)
		e := engine{pat: q.r, txt: []rune(s), starP: -1}
		if e.run() {
			t.Fatalf("unexpected match at m=%d", m)
		}
		if e.steps > linearFactor*(n+m) {
			t.Fatalf("m=%d steps=%d exceeds %d*(n+m)=%d",
				m, e.steps, linearFactor, linearFactor*(n+m))
		}
		// Ratio to n*m must collapse as m grows (not quadratic in m).
		if prev > 0 && e.steps >= prev*20 {
			t.Fatalf("steps grew super-linearly: %d -> %d for 10x m", prev, e.steps)
		}
		prev = e.steps
	}
}
