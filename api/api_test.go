package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/reast"
)

// TestMatchVsBacktracking pins invariant (1): NFA == naive AST backtracking.
func TestMatchVsBacktracking(t *testing.T) {
	fixed := [][2]string{
		{"a", "a"}, {"a", "b"}, {"ab", "ab"}, {"a|b", "b"},
		{"ab|cd", "ab"}, {"ab|cd", "abd"}, {"a*", ""}, {"a+", ""},
		{"ab*", "a"}, {"(ab)*", "a"}, {"a(b|c)*", "abb"}, {"(ab)+", "abab"},
	}
	for _, c := range fixed {
		re, err := reast.Parse(c[0])
		if err != nil {
			t.Fatalf("parse %q: %v", c[0], err)
		}
		if got, _ := Match(c[0], c[1]); got != naiveAccept(re.Root, c[1]) {
			t.Fatalf("%q vs %q: nfa=%v ref=%v", c[0], c[1], got, !got)
		}
	}
	rng := rand.New(rand.NewSource(659))
	for iter := 0; iter < 300; iter++ {
		p := randPattern(rng, 1+rng.Intn(5))
		re, err := reast.Parse(p)
		if err != nil {
			t.Fatalf("random %q: %v", p, err)
		}
		for j := 0; j < 6; j++ { // 'd' never appears in generated patterns
			s := randString(rng, rng.Intn(6))
			if got, _ := Match(p, s); got != naiveAccept(re.Root, s) {
				t.Fatalf("random %q vs %q: nfa=%v ref=%v", p, s, got, !got)
			}
		}
	}
}

// TestSuffixSemantics pins invariant (3): R* takes eps, R+ == R.R*, R? == {eps}|L(R).
func TestSuffixSemantics(t *testing.T) {
	for _, q := range []string{"a+", "(ab)+", "(ab|c)+"} {
		b := q[:len(q)-1]
		n1, _ := Compile(q)
		n2, _ := Compile(b + b + "*")
		rng := rand.New(rand.NewSource(7))
		for i := 0; i < 64; i++ {
			if s := randString(rng, rng.Intn(6)); n1.Match(s) != n2.Match(s) {
				t.Fatalf("%q vs expansion disagree on %q", q, s)
			}
		}
	}
	for _, q := range []string{"a?", "(ab|c)?", "(x|y)?"} {
		in, _ := reast.Parse(q[:len(q)-1])
		n, _ := Compile(q)
		rng := rand.New(rand.NewSource(8))
		for i := 0; i < 64; i++ {
			s := randString(rng, rng.Intn(5))
			if want := s == "" || naiveAccept(in.Root, s); n.Match(s) != want {
				t.Fatalf("%q vs %q: got %v want %v", q, s, n.Match(s), want)
			}
		}
	}
	for _, q := range []string{"a*", "(ab)*", "(a|b)*", "(b|c)*"} {
		n, _ := Compile(q)
		if !n.Match("") {
			t.Fatalf("%q must accept empty", q)
		}
	}
}

// TestErrorsDistinctAndNoTrace pins invariant (4): four distinct sentinels,
// nil NFA on failure, later calls unaffected.
func TestErrorsDistinctAndNoTrace(t *testing.T) {
	bad := []string{"", "A", "a1", "(a", "a)", "((a)", "(", "*a", "(|a)*", "a|", "(|)"}
	want := []error{ErrEmptyPattern, ErrIllegalChar, ErrIllegalChar,
		ErrUnbalancedParen, ErrUnbalancedParen, ErrUnbalancedParen, ErrUnbalancedParen,
		ErrDanglingPostfix, ErrDanglingPostfix, ErrDanglingPostfix, ErrDanglingPostfix}
	seen := map[error]bool{}
	for i, p := range bad {
		n, err := Compile(p)
		if n != nil || !errors.Is(err, want[i]) {
			t.Fatalf("%q: got (%v,%v), want %v", p, n, err, want[i])
		}
		seen[want[i]] = true
	}
	if len(seen) != 4 {
		t.Fatalf("want 4 distinct sentinels, got %d", len(seen))
	}
	if ok, err := Match("a", "a"); !ok || err != nil {
		t.Fatalf("later call broken after failures: %v", err)
	}
}

// TestConcurrentMatch pins section (6): concurrent Match on one NFA agrees; race.
func TestConcurrentMatch(t *testing.T) {
	n, _ := Compile("(a|b)*c?(ab)+")
	strs := []string{"", "a", "ab", "abab", "abcab", "babab", "abx", "ccab", "ababab"}
	base := make([]bool, len(strs))
	for i, s := range strs {
		base[i] = n.Match(s)
	}
	const G = 64
	var wg sync.WaitGroup
	res := make([][]bool, G)
	for g := 0; g < G; g++ {
		wg.Add(1)
		res[g] = make([]bool, len(strs))
		go func(idx int) {
			defer wg.Done()
			for i, s := range strs {
				res[idx][i] = n.Match(s)
			}
		}(g)
	}
	wg.Wait()
	for g := 0; g < G; g++ {
		for i := range strs {
			if res[g][i] != base[i] {
				t.Fatalf("goroutine %d %q: %v want %v", g, strs[i], res[g][i], base[i])
			}
		}
	}
}

func randPattern(rng *rand.Rand, depth int) string {
	if depth <= 0 || rng.Intn(3) == 0 {
		return string("abc"[rng.Intn(3)])
	}
	x := randPattern(rng, depth-1)
	if rng.Intn(2) == 0 && len(x) > 1 {
		x = "(" + x + ")"
	}
	if d := rng.Intn(4); d < 3 {
		return x + "*+?"[d:d+1]
	}
	return x + randPattern(rng, depth-1)
}

func randString(rng *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = "abcd"[rng.Intn(4)]
	}
	return string(b)
}
